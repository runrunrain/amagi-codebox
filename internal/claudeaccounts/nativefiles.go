package claudeaccounts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/tidwall/gjson"
)

// ClaudeKeychainAccess 是 Claude Code 凭据 Keychain 项的访问边界。
// 生产实现为 macOS Keychain（nativecc_darwin.go）；测试注入 fake，
// 绝不触碰真实 Keychain。
type ClaudeKeychainAccess interface {
	// LocateItem 按 service 查唯一匹配项；零项报缺失、多项报歧义
	// （design §3.1）。
	LocateItem(ctx context.Context) (*KeychainLocator, error)
	// ReadItem 读取项完整字节。
	ReadItem(ctx context.Context, loc *KeychainLocator) ([]byte, error)
	// UpdateItem 原位更新项内容（保留既有 ACL，不 delete+add）。
	UpdateItem(ctx context.Context, loc *KeychainLocator, item []byte) error
}

// FileCredentialPaths 是两处原生凭据文件的注入路径（design §10.1：测试
// 临时目录必须通过依赖注入覆盖所有路径）。
type FileCredentialPaths struct {
	ClaudeJSONPath string // 默认 ~/.claude.json
	PiAuthPath     string // 默认 ~/.pi/agent/auth.json
}

// DefaultFileCredentialPaths 返回 canonical 路径（design §4：标准 agent
// root 固定，PI_CODING_AGENT_DIR 不参与凭据读写判断）。
func DefaultFileCredentialPaths() (FileCredentialPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return FileCredentialPaths{}, err
	}
	return FileCredentialPaths{
		ClaudeJSONPath: filepath.Join(home, ".claude.json"),
		PiAuthPath:     filepath.Join(home, ".pi", "agent", "auth.json"),
	}, nil
}

// FileCredentialStore 是 `.claude.json` 与 `~/.pi/agent/auth.json` 的字段级
// patch 适配器。写入全部走原子写（nativefile.go），写入前后做回读验证。
type FileCredentialStore struct {
	paths FileCredentialPaths
	ops   writeFileOps
}

// NewFileCredentialStore 构造文件投影适配器；ops 为 nil 时使用真实文件系统。
func NewFileCredentialStore(paths FileCredentialPaths, ops writeFileOps) *FileCredentialStore {
	if ops == nil {
		ops = osWriteFileOps{}
	}
	return &FileCredentialStore{paths: paths, ops: ops}
}

// Paths 返回注入的路径（仅供接线层与测试）。
func (s *FileCredentialStore) Paths() FileCredentialPaths { return s.paths }

// ClaudeJSONState 是 `.claude.json` 的受管投影读取结果。
type ClaudeJSONState struct {
	Fields     ClaudeIdentityFields
	Revision   FileRevision
	OtherTopKeys []string
}

// ReadClaudeJSON 读取 `.claude.json` 的受管字段（oauthAccount + userID）。
func (s *FileCredentialStore) ReadClaudeJSON(ctx context.Context) (*ClaudeJSONState, error) {
	_ = ctx
	data, rev, err := readStableFile(s.paths.ClaudeJSONPath)
	if err != nil {
		return nil, err
	}
	if err := validateJSONBytes(data, ".claude.json"); err != nil {
		return nil, err
	}
	oauth := gjson.GetBytes(data, "oauthAccount")
	userID := gjson.GetBytes(data, "userID")
	if !oauth.Exists() || !oauth.IsObject() {
		return nil, NewErr(ReasonNeedsLogin, ".claude.json 缺少 oauthAccount（未登录 Claude Code）")
	}
	if !userID.Exists() || userID.Type != gjson.String {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, ".claude.json 缺少 userID 字符串字段")
	}
	fields := ClaudeIdentityFields{OauthAccount: canonicalJSON([]byte(oauth.Raw)), UserID: userID.String()}
	rev = fileRevisionWithManaged(rev, managedClaudeIdentityHash(fields))
	return &ClaudeJSONState{
		Fields:       fields,
		Revision:     rev,
		OtherTopKeys: topKeyNames(data),
	}, nil
}

// PatchClaudeIdentity 字段级替换 oauthAccount 与 userID（design §1.3-4）。
// 其他顶层字段与原字节片段保留（sjson 字段级 patch）；写入后回读验证。
func (s *FileCredentialStore) PatchClaudeIdentity(ctx context.Context, fields ClaudeIdentityFields) (*FileRevision, error) {
	_ = ctx
	before, err := s.ReadClaudeJSON(ctx)
	if err != nil {
		return nil, err
	}
	if len(fields.OauthAccount) == 0 || !gjson.ValidBytes(fields.OauthAccount) {
		return nil, NewErr(ReasonUnsupportedCredentialSchema, "oauthAccount 替换值不是合法 JSON 对象")
	}
	data, _, err := readStableFile(s.paths.ClaudeJSONPath)
	if err != nil {
		return nil, err
	}
	next, err := sjsonSetRawBytes(data, "oauthAccount", fields.OauthAccount)
	if err != nil {
		return nil, NewErr(ReasonNeedsReconcile, "oauthAccount 字段替换失败")
	}
	userQuoted, err := json.Marshal(fields.UserID)
	if err != nil {
		return nil, NewErr(ReasonNeedsReconcile, "userID 编码失败")
	}
	next, err = sjsonSetRawBytes(next, "userID", userQuoted)
	if err != nil {
		return nil, NewErr(ReasonNeedsReconcile, "userID 字段替换失败")
	}
	if _, err := writeAtomicFile(s.paths.ClaudeJSONPath, next, s.ops); err != nil {
		return nil, err
	}
	return s.verifyClaudeJSON(ctx, fields, before.OtherTopKeys)
}

func (s *FileCredentialStore) verifyClaudeJSON(ctx context.Context, want ClaudeIdentityFields, beforeKeys []string) (*FileRevision, error) {
	after, err := s.ReadClaudeJSON(ctx)
	if err != nil {
		return nil, err
	}
	if !equalJSON(after.Fields.OauthAccount, want.OauthAccount) || after.Fields.UserID != want.UserID {
		return nil, NewErr(ReasonNeedsReconcile, ".claude.json 回读与期望不符")
	}
	if !sameStringSet(beforeKeys, after.OtherTopKeys) {
		return nil, NewErr(ReasonNeedsReconcile, ".claude.json 顶层键集合在写入后变化")
	}
	return &after.Revision, nil
}

// PiAuthState 是 auth.json 的受管投影读取结果。
type PiAuthState struct {
	Anthropic  json.RawMessage
	Revision   FileRevision
	OtherTopKeys []string
}

// ReadPiAuth 读取 auth.json 的 anthropic 条目（仅此一键）。
func (s *FileCredentialStore) ReadPiAuth(ctx context.Context) (*PiAuthState, error) {
	_ = ctx
	data, rev, err := readStableFile(s.paths.PiAuthPath)
	if err != nil {
		return nil, err
	}
	if err := validateJSONBytes(data, "auth.json"); err != nil {
		return nil, err
	}
	anthropic := gjson.GetBytes(data, "anthropic")
	if !anthropic.Exists() || !anthropic.IsObject() {
		return nil, NewErr(ReasonNeedsLogin, "auth.json 缺少 anthropic oauth 条目（未登录 Pi）")
	}
	raw := canonicalJSON([]byte(anthropic.Raw))
	if err := ValidatePiAnthropicEntry(raw); err != nil {
		return nil, err
	}
	rev = fileRevisionWithManaged(rev, raw)
	return &PiAuthState{Anthropic: raw, Revision: rev, OtherTopKeys: topKeyNames(data)}, nil
}

// PatchPiAnthropic 仅替换 auth.json 的 anthropic 键（design §1.3-4），
// 其他 provider 条目原样保留；写入后回读验证。
func (s *FileCredentialStore) PatchPiAnthropic(ctx context.Context, anthropic json.RawMessage) (*FileRevision, error) {
	_ = ctx
	if err := ValidatePiAnthropicEntry(anthropic); err != nil {
		return nil, err
	}
	before, err := s.ReadPiAuth(ctx)
	if err != nil {
		return nil, err
	}
	data, _, err := readStableFile(s.paths.PiAuthPath)
	if err != nil {
		return nil, err
	}
	next, err := sjsonSetRawBytes(data, "anthropic", anthropic)
	if err != nil {
		return nil, NewErr(ReasonNeedsReconcile, "auth.json anthropic 字段替换失败")
	}
	if _, err := writeAtomicFile(s.paths.PiAuthPath, next, s.ops); err != nil {
		return nil, err
	}
	return s.verifyPiAuth(ctx, anthropic, before.OtherTopKeys)
}

func (s *FileCredentialStore) verifyPiAuth(ctx context.Context, want json.RawMessage, beforeKeys []string) (*FileRevision, error) {
	after, err := s.ReadPiAuth(ctx)
	if err != nil {
		return nil, err
	}
	if !equalJSON(after.Anthropic, want) {
		return nil, NewErr(ReasonNeedsReconcile, "auth.json 回读与期望不符")
	}
	if !sameStringSet(beforeKeys, after.OtherTopKeys) {
		return nil, NewErr(ReasonNeedsReconcile, "auth.json 顶层键集合在写入后变化")
	}
	return &after.Revision, nil
}

// CombinedNativeStore 组合文件投影与 Claude Code Keychain 访问，实现
// NativeCredentialStore 的生产形态（测试可用真实 tempdir 文件 + fake
// Keychain 组合）。
type CombinedNativeStore struct {
	files    *FileCredentialStore
	keychain ClaudeKeychainAccess

	mu      sync.Mutex
	locator *KeychainLocator
}

// NewCombinedNativeStore 构造组合适配器。
func NewCombinedNativeStore(files *FileCredentialStore, keychain ClaudeKeychainAccess) *CombinedNativeStore {
	return &CombinedNativeStore{files: files, keychain: keychain}
}

// Kind 返回后端标识。
func (c *CombinedNativeStore) Kind() string { return "combined" }

// locatedItem 返回缓存的 locator；缓存失效（项被删）时重新定位一次。
func (c *CombinedNativeStore) locatedItem(ctx context.Context) (*KeychainLocator, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locator != nil {
		if _, err := c.keychain.ReadItem(ctx, c.locator); err == nil {
			return c.locator, nil
		} else if ReasonOf(err) != ReasonNeedsLogin {
			return nil, err
		}
		// 项消失：重新定位（可能被外部重建为不同 account）。
	}
	loc, err := c.keychain.LocateItem(ctx)
	if err != nil {
		return nil, err
	}
	c.locator = loc
	return loc, nil
}

// ReadStable 读取三处受管投影的稳定快照。
func (c *CombinedNativeStore) ReadStable(ctx context.Context) (*NativeProjections, error) {
	loc, err := c.locatedItem(ctx)
	if err != nil {
		return nil, err
	}
	item, err := c.keychain.ReadItem(ctx, loc)
	if err != nil {
		return nil, err
	}
	if err := validateJSONBytes(item, "Claude Code Keychain 项"); err != nil {
		return nil, err
	}
	oauth, err := ExtractClaudeOAuthProjection(item)
	if err != nil {
		return nil, err
	}
	cj, err := c.files.ReadClaudeJSON(ctx)
	if err != nil {
		return nil, err
	}
	pa, err := c.files.ReadPiAuth(ctx)
	if err != nil {
		return nil, err
	}
	return &NativeProjections{
		ClaudeOAuth:     oauth,
		ClaudeOAuthRaw:  json.RawMessage(item),
		Locator:         loc,
		ClaudeIdentity:  &cj.Fields,
		PiAnthropic:     pa.Anthropic,
		PiOtherProviders: pa.OtherTopKeys,
		ClaudeJSONOtherTopKeys: cj.OtherTopKeys,
		Revisions: ExpectedRevisions{
			ClaudeKeychain: &FileRevision{SHA256Hex: HashRevision(item), ManagedHash: HashRevision(oauth)},
			ClaudeJSON:     &cj.Revision,
			PiAuth:         &pa.Revision,
		},
	}, nil
}

// PatchClaudeKeychainOAuth 替换 Keychain 项内的 OAuth 投影，保留未知顶层
// 兄弟字段；写入后回读验证。
func (c *CombinedNativeStore) PatchClaudeKeychainOAuth(ctx context.Context, oauth json.RawMessage) (*FileRevision, error) {
	loc, err := c.locatedItem(ctx)
	if err != nil {
		return nil, err
	}
	item, err := c.keychain.ReadItem(ctx, loc)
	if err != nil {
		return nil, err
	}
	next, err := PatchClaudeKeychainItemBytes(item, oauth)
	if err != nil {
		return nil, err
	}
	if err := c.keychain.UpdateItem(ctx, loc, next); err != nil {
		return nil, err
	}
	reloaded, err := c.keychain.ReadItem(ctx, loc)
	if err != nil {
		return nil, err
	}
	got, err := ExtractClaudeOAuthProjection(reloaded)
	if err != nil {
		return nil, err
	}
	if !equalJSON(got, oauth) {
		return nil, NewErr(ReasonNeedsReconcile, "Claude Code Keychain 回读投影与期望不符")
	}
	// 非拥有顶层兄弟字段保持不变（design §3.1：不跨账号复制未知字段）。
	if !sameStringSet(topKeyNames(item), topKeyNames(reloaded)) {
		return nil, NewErr(ReasonNeedsReconcile, "Claude Code Keychain 顶层字段集合在写入后变化")
	}
	rev := FileRevision{SHA256Hex: HashRevision(reloaded), ManagedHash: HashRevision(got)}
	return &rev, nil
}

// PatchClaudeIdentity 代理到文件适配器。
func (c *CombinedNativeStore) PatchClaudeIdentity(ctx context.Context, fields ClaudeIdentityFields) (*FileRevision, error) {
	return c.files.PatchClaudeIdentity(ctx, fields)
}

// PatchPiAnthropic 代理到文件适配器。
func (c *CombinedNativeStore) PatchPiAnthropic(ctx context.Context, anthropic json.RawMessage) (*FileRevision, error) {
	return c.files.PatchPiAnthropic(ctx, anthropic)
}

// managedClaudeIdentityHash 计算受管身份字段的组合 hash（长度前缀消除
// 分隔歧义，oauthAccount 与 userID 都参与）。
func managedClaudeIdentityHash(fields ClaudeIdentityFields) []byte {
	buf := strconv.AppendInt(make([]byte, 0, len(fields.OauthAccount)+len(fields.UserID)+32), int64(len(fields.OauthAccount)), 10)
	buf = append(buf, ':')
	buf = append(buf, fields.OauthAccount...)
	buf = append(buf, '|')
	buf = append(buf, fields.UserID...)
	return buf
}
