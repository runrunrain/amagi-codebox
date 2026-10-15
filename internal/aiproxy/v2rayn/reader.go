// Package v2rayn reads v2rayN's local SQLite database without downloading feeds.
package v2rayn

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"amagi-codebox/internal/aiproxy/subscription"
	_ "modernc.org/sqlite"
)

type Group struct {
	ID       string
	Name     string
	Total    int
	Nodes    []subscription.Node
	Warnings []string
}

func Locate(explicit string) (string, error) {
	if explicit != "" {
		p, err := filepath.Abs(explicit)
		if err != nil {
			return "", errors.New("v2rayN 数据库路径无效")
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return "", errors.New("找不到指定的 v2rayN 数据库")
		}
		return p, nil
	}
	home, _ := os.UserHomeDir()
	paths := []string{
		filepath.Join(home, "Library/Application Support/v2rayN/guiConfigs/guiNDB.db"),
		filepath.Join(home, ".local/share/v2rayN/guiConfigs/guiNDB.db"),
		filepath.Join(home, ".config/v2rayN/guiConfigs/guiNDB.db"),
	}
	for _, base := range []string{os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA"), os.Getenv("XDG_DATA_HOME"), os.Getenv("XDG_CONFIG_HOME")} {
		if base != "" {
			paths = append(paths, filepath.Join(base, "v2rayN/guiConfigs/guiNDB.db"))
		}
	}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "guiConfigs/guiNDB.db"))
	}
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", nil
}

func openReadOnly(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	u.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)"}}.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func Read(ctx context.Context, path string) ([]Group, error) {
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return nil, errors.New("找不到 v2rayN 数据库；可通过 --v2rayn-db 指定 guiNDB.db")
	}
	db, err := openReadOnly(path)
	if err != nil {
		return nil, errors.New("无法以只读方式打开 v2rayN 数据库")
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, errors.New("v2rayN 数据库暂时无法读取，请稍后重试")
	}
	defer tx.Rollback()
	subs, err := readRows(ctx, tx, "SELECT Id, Remarks FROM SubItem")
	if err != nil {
		return nil, errors.New("无法读取 v2rayN 订阅表，数据库版本可能不兼容")
	}
	profiles, err := readRows(ctx, tx, "SELECT * FROM ProfileItem")
	if err != nil {
		return nil, errors.New("无法读取 v2rayN 节点表，数据库版本可能不兼容")
	}
	groups := map[string]*Group{}
	for _, sub := range subs {
		id := value(sub["Id"])
		if id == "" {
			continue
		}
		name := value(sub["Remarks"])
		if name == "" {
			name = "未命名订阅"
		}
		groups[id] = &Group{ID: id, Name: name, Nodes: []subscription.Node{}, Warnings: []string{}}
	}
	for _, profile := range profiles {
		id := value(profile["Subid"])
		g := groups[id]
		if g == nil {
			name := "未分组节点"
			if id != "" {
				name = "其他节点分组"
			}
			g = &Group{ID: id, Name: name, Nodes: []subscription.Node{}, Warnings: []string{}}
			groups[id] = g
		}
		g.Total++
		node, err := convert(profile)
		if err != nil {
			g.Warnings = append(g.Warnings, fmt.Sprintf("第 %d 个本地节点：%v", g.Total, err))
			continue
		}
		g.Nodes = append(g.Nodes, node)
	}
	result := make([]Group, 0, len(groups))
	for _, g := range groups {
		seen := map[string]bool{}
		unique := g.Nodes[:0]
		for _, n := range g.Nodes {
			if !seen[n.ID] {
				unique = append(unique, n)
				seen[n.ID] = true
			}
		}
		g.Nodes = unique
		result = append(result, *g)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}

func readRows(ctx context.Context, tx *sql.Tx, query string) ([]map[string]any, error) {
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for rows.Next() {
		if len(result) >= 20000 {
			return nil, errors.New("v2rayN 记录数超过读取上限")
		}
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range ptrs {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, c := range cols {
			if b, ok := values[i].([]byte); ok {
				row[c] = string(b)
			} else {
				row[c] = values[i]
			}
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
