// Package aiproxy owns CodeBox's independent AI proxy routing feature.
// Raw nodes and subscription credentials stay behind the desktop facade.
package aiproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"amagi-codebox/internal/aiproxy/router"
	"amagi-codebox/internal/aiproxy/subscription"
	"amagi-codebox/internal/aiproxy/v2rayn"
)

type Settings struct {
	Routing        router.Settings `json:"routing"`
	CorePath       string          `json:"corePath"`
	V2rayNDatabase string          `json:"v2raynDatabase"`
}

func (s Settings) clone() Settings {
	s.Routing = s.Routing.Clone()
	return s
}

type Node struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
type Group struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Nodes       []Node   `json:"nodes"`
	Warnings    []string `json:"warnings"`
	Refreshable bool     `json:"refreshable"`
}
type SourceStatus struct {
	Available bool   `json:"available"`
	Database  string `json:"database"`
	Groups    int    `json:"groups"`
	Nodes     int    `json:"nodes"`
	Skipped   int    `json:"skipped"`
	Error     string `json:"error"`
}
type State struct {
	Enabled  bool          `json:"enabled"`
	Pending  bool          `json:"pending"`
	Settings Settings      `json:"settings"`
	Groups   []Group       `json:"groups"`
	Status   router.Status `json:"status"`
	Source   SourceStatus  `json:"source"`
	Error    string        `json:"error"`
}
type storedGroup struct {
	ID       string              `json:"id"`
	Name     string              `json:"name"`
	URL      string              `json:"url,omitempty"`
	Source   string              `json:"source,omitempty"`
	Nodes    []subscription.Node `json:"nodes"`
	Warnings []string            `json:"warnings"`
}
type storedState struct {
	Version  int           `json:"version"`
	Enabled  bool          `json:"enabled"`
	Revision uint64        `json:"revision"`
	Settings Settings      `json:"settings"`
	Groups   []storedGroup `json:"groups"`
}
type Service struct {
	mu      sync.Mutex
	dir     string
	data    storedState
	manager *router.Manager
	active  Settings
	source  SourceStatus
	loadErr string
	closed  bool
}

func New(dir string) *Service {
	return &Service{dir: dir, data: storedState{Version: 2, Revision: 1, Settings: Settings{Routing: router.Defaults()}, Groups: []storedGroup{}}}
}

func (s *Service) Load(ctx context.Context) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("AI 代理分流已关闭")
	}
	b, err := os.ReadFile(filepath.Join(s.dir, "state.json"))
	if err == nil {
		data, migrated, decodeErr := decodeStoredState(b)
		if decodeErr != nil {
			s.loadErr = "AI 代理配置损坏或版本不兼容，请修复配置后重启"
			s.mu.Unlock()
			return errors.New(s.loadErr)
		}
		s.data = data
		s.loadErr = ""
		if migrated {
			if err := s.saveLocked(); err != nil {
				s.loadErr = "AI 代理配置迁移失败：" + err.Error()
				s.mu.Unlock()
				return errors.New(s.loadErr)
			}
		}
	} else if !os.IsNotExist(err) {
		s.loadErr = "无法读取 AI 代理配置"
		s.mu.Unlock()
		return errors.New(s.loadErr)
	}
	s.manager = router.NewManager(router.FindCore(s.data.Settings.CorePath), filepath.Join(s.dir, "runtime"))
	s.loadErr = ""
	restore := s.data.Enabled
	s.mu.Unlock()
	// Read local node snapshots without fetching subscription URLs.
	_, _ = s.SyncV2rayN(ctx)
	if restore {
		return s.Start(ctx)
	}
	return nil
}
func (s *Service) saveLocked() error {
	if s.loadErr != "" {
		return errors.New(s.loadErr)
	}
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(s.dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return router.AtomicWrite(filepath.Join(s.dir, "state.json"), b)
}
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	view := State{Enabled: s.data.Enabled, Settings: s.data.Settings.clone(), Groups: []Group{}, Source: s.source, Error: s.loadErr}
	view.Settings.Routing.FetchProxy = maskProxy(view.Settings.Routing.FetchProxy)
	view.Settings.Routing.FallbackProxy = maskProxy(view.Settings.Routing.FallbackProxy)
	if s.manager != nil {
		view.Status = s.manager.Status()
		view.Pending = view.Status.Running && view.Status.Revision != s.data.Revision
	} else {
		view.Status.Core = router.FindCore(s.data.Settings.CorePath)
	}
	for _, g := range s.data.Groups {
		group := Group{ID: g.ID, Name: g.Name, Source: g.Source, Warnings: append([]string{}, g.Warnings...), Refreshable: g.URL != "", Nodes: []Node{}}
		for _, n := range g.Nodes {
			group.Nodes = append(group.Nodes, Node{ID: g.ID + ":" + n.ID, Name: n.Name, Type: n.Type})
		}
		view.Groups = append(view.Groups, group)
	}
	return view
}

const masked = "(已保存带认证代理，保持此值可保留)"

func maskProxy(raw string) string {
	u, err := url.Parse(raw)
	if err == nil && u.User != nil {
		return masked
	}
	return raw
}
func (s *Service) SaveSettings(settings Settings) error {
	settings = settings.clone()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != "" {
		return errors.New(s.loadErr)
	}
	if s.closed {
		return errors.New("AI 代理分流已关闭")
	}
	if settings.Routing.FetchProxy == masked {
		settings.Routing.FetchProxy = s.data.Settings.Routing.FetchProxy
	}
	if settings.Routing.FallbackProxy == masked {
		settings.Routing.FallbackProxy = s.data.Settings.Routing.FallbackProxy
	}
	if err := settings.Routing.Validate(); err != nil {
		return err
	}
	if s.manager != nil && s.manager.Status().Running && settings.CorePath != s.data.Settings.CorePath {
		return errors.New("更换内核路径前请先停止 AI 代理")
	}
	nodes := s.nodesLocked()
	for _, rule := range settings.Routing.Rules {
		if rule.Enabled && rule.Mode == "proxy" && rule.Node != "" {
			if _, ok := nodes[rule.Node]; !ok {
				return fmt.Errorf("规则 %q 的节点已失效，请重新选择", rule.Name)
			}
		}
	}
	before, _ := json.Marshal(s.data.Settings)
	after, _ := json.Marshal(settings)
	if string(before) == string(after) {
		return nil
	}
	old := s.data
	s.data.Settings = settings
	s.data.Revision++
	if err := s.saveLocked(); err != nil {
		s.data = old
		return err
	}
	return nil
}
func (s *Service) nodesLocked() map[string]subscription.Node {
	nodes := map[string]subscription.Node{}
	for _, g := range s.data.Groups {
		for _, n := range g.Nodes {
			nodes[g.ID+":"+n.ID] = n
		}
	}
	return nodes
}
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("AI 代理分流已关闭")
	}
	if s.loadErr != "" {
		return errors.New(s.loadErr)
	}
	config, err := router.Build(s.data.Settings.Routing, s.nodesLocked())
	if err != nil {
		return err
	}
	core := router.FindCore(s.data.Settings.CorePath)
	if s.manager == nil || s.manager.Status().Core != core {
		s.manager = router.NewManager(core, filepath.Join(s.dir, "runtime"))
	}
	// Commit the enabled preference before replacing a live process. A storage
	// failure must never leave new listeners paired with the old launch ports.
	wasEnabled := s.data.Enabled
	if !wasEnabled {
		s.data.Enabled = true
		if err = s.saveLocked(); err != nil {
			s.data.Enabled = false
			return fmt.Errorf("保存 AI 代理启用状态失败：%w", err)
		}
	}
	if err = s.manager.Apply(ctx, config, s.data.Settings.Routing.Ports(), s.data.Revision); err != nil {
		if !wasEnabled {
			s.data.Enabled = false
			if saveErr := s.saveLocked(); saveErr != nil {
				return fmt.Errorf("%w；恢复停用状态失败：%v", err, saveErr)
			}
		}
		return err
	}
	s.active = s.data.Settings.clone()
	return nil
}
func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data.Enabled
	s.data.Enabled = false
	if err := s.saveLocked(); err != nil {
		s.data.Enabled = old
		return err
	}
	if s.manager != nil {
		return s.manager.Stop()
	}
	return nil
}

// Close stops the owned core but retains the user's enabled preference.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.manager != nil {
		return s.manager.Stop()
	}
	return nil
}

func (s *Service) SyncV2rayN(ctx context.Context) (SourceStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.source, errors.New("AI 代理分流已关闭")
	}
	if s.loadErr != "" {
		return s.source, errors.New(s.loadErr)
	}
	path, err := v2rayn.Locate(s.data.Settings.V2rayNDatabase)
	s.source = SourceStatus{Available: path != "", Database: path}
	if err == nil && path == "" {
		err = errors.New("未找到本机 v2rayN，可设置数据库路径或手动导入订阅")
	}
	if err != nil {
		s.source.Error = err.Error()
		return s.source, err
	}
	groups, err := v2rayn.Read(ctx, path)
	if err != nil {
		s.source.Error = err.Error()
		return s.source, err
	}
	old := s.data
	next := append([]storedGroup{}, s.data.Groups...)
	changed := false
	for _, g := range groups {
		s.source.Skipped += g.Total - len(g.Nodes)
		if len(g.Nodes) == 0 {
			continue
		}
		s.source.Groups++
		s.source.Nodes += len(g.Nodes)
		h := sha256.Sum256([]byte(path + "\x00" + g.ID))
		id := "v2rayn-" + hex.EncodeToString(h[:12])
		group := storedGroup{ID: id, Name: g.Name, Source: "v2rayn", Nodes: g.Nodes, Warnings: g.Warnings}
		index := -1
		for i := range next {
			if next[i].ID == id {
				index = i
				break
			}
		}
		if index >= 0 {
			before, _ := json.Marshal(next[index])
			after, _ := json.Marshal(group)
			if string(before) == string(after) {
				continue
			}
			next[index] = group
		} else {
			next = append(next, group)
		}
		changed = true
	}
	if changed {
		s.data.Groups = next
		s.data.Revision++
		if err = s.saveLocked(); err != nil {
			s.data = old
			return s.source, err
		}
	}
	return s.source, nil
}
func (s *Service) Import(ctx context.Context, name, rawURL, content string) error {
	name, rawURL, content = strings.TrimSpace(name), strings.TrimSpace(rawURL), strings.TrimSpace(content)
	if name == "" || len(name) > 200 {
		return errors.New("请填写订阅名称")
	}
	if (rawURL == "") == (content == "") {
		return errors.New("订阅 URL 与节点内容请二选一")
	}
	s.mu.Lock()
	proxy := s.data.Settings.Routing.FetchProxy
	s.mu.Unlock()
	b := []byte(content)
	var err error
	if rawURL != "" {
		b, err = fetch(ctx, rawURL, proxy)
		if err != nil {
			return err
		}
	}
	nodes, warnings, err := subscription.Parse(b)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("AI 代理分流已关闭")
	}
	h := sha256.Sum256([]byte(name + "\x00" + rawURL + "\x00" + content))
	id := "import-" + hex.EncodeToString(h[:12])
	group := storedGroup{ID: id, Name: name, URL: rawURL, Nodes: nodes, Warnings: warnings}
	old := s.data
	next := append([]storedGroup{}, s.data.Groups...)
	index := -1
	for i := range next {
		if next[i].ID == id {
			index = i
			break
		}
	}
	if index >= 0 {
		next[index] = group
	} else {
		next = append(next, group)
	}
	s.data.Groups = next
	s.data.Revision++
	if err = s.saveLocked(); err != nil {
		s.data = old
		return err
	}
	return nil
}
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data
	next := []storedGroup{}
	found := false
	for _, g := range s.data.Groups {
		if g.ID == id {
			found = true
			continue
		}
		next = append(next, g)
	}
	if !found {
		return errors.New("订阅分组不存在")
	}
	s.data.Groups = next
	s.data.Revision++
	s.data.Settings = s.data.Settings.clone()
	for i := range s.data.Settings.Routing.Rules {
		if strings.HasPrefix(s.data.Settings.Routing.Rules[i].Node, id+":") {
			s.data.Settings.Routing.Rules[i].Node = ""
		}
	}
	if err := s.saveLocked(); err != nil {
		s.data = old
		return err
	}
	return nil
}
func (s *Service) Refresh(ctx context.Context, id string) error {
	s.mu.Lock()
	var group storedGroup
	for _, g := range s.data.Groups {
		if g.ID == id {
			group = g
			break
		}
	}
	proxy := s.data.Settings.Routing.FetchProxy
	s.mu.Unlock()
	if group.Source == "v2rayn" {
		_, err := s.SyncV2rayN(ctx)
		return err
	}
	if group.URL == "" {
		return errors.New("此分组没有订阅 URL")
	}
	b, err := fetch(ctx, group.URL, proxy)
	if err != nil {
		return err
	}
	nodes, warnings, err := subscription.Parse(b)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.data
	next := append([]storedGroup{}, s.data.Groups...)
	found := false
	for i := range next {
		if next[i].ID == id {
			next[i].Nodes = nodes
			next[i].Warnings = warnings
			found = true
		}
	}
	if !found {
		return errors.New("订阅分组已删除")
	}
	s.data.Groups = next
	s.data.Revision++
	if err = s.saveLocked(); err != nil {
		s.data = old
		return err
	}
	return nil
}
func fetch(ctx context.Context, raw, proxy string) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("订阅地址应为 HTTP(S) URL")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	if proxy != "" {
		p, _ := url.Parse(proxy)
		tr.Proxy = http.ProxyURL(p)
	}
	defer tr.CloseIdleConnections()
	client := http.Client{Transport: tr, Timeout: 25 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") || (via[0].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return errors.New("invalid redirect")
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, errors.New("订阅地址无效")
	}
	req.Header.Set("User-Agent", "Amagi-CodeBox-AI-Proxy")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("订阅下载失败，请检查网络和下载代理")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("订阅服务器返回 HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, subscription.MaxBytes+1))
	if err != nil || len(b) > subscription.MaxBytes {
		return nil, errors.New("订阅读取失败或超过 8 MiB")
	}
	return b, nil
}
