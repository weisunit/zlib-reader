// s-library: 书籍详情与可用格式（FR-3.6），含个人下载域名解析（FR-2.4）。
package zlibrary

import (
	"context"
	"net/url"
)

// Book 获取书籍详情（含 descr 与 dl 个人下载地址）。
func (c *Client) Book(ctx context.Context, id, hash string) (*ZBook, error) {
	if id == "" || hash == "" {
		return nil, NewZError(CodeNotFound, "书籍标识缺失", nil)
	}
	m, err := c.getJSON(ctx, "/eapi/book/"+urlPathEscape(id)+"/"+urlPathEscape(hash))
	if err != nil {
		return nil, err
	}
	bookMap, _ := m["book"].(map[string]any)
	if bookMap == nil {
		return nil, NewZError(CodeNotFound, "书籍不存在或已下架", nil)
	}
	b := parseBook(bookMap)
	if b.ID == "" {
		b.ID = id
	}
	if b.Hash == "" {
		b.Hash = hash
	}
	return &b, nil
}

// Formats 获取多格式书籍的格式列表。
func (c *Client) Formats(ctx context.Context, id, hash string) ([]ZFormat, error) {
	m, err := c.getJSON(ctx, "/eapi/book/"+urlPathEscape(id)+"/"+urlPathEscape(hash)+"/formats")
	if err != nil {
		return nil, err
	}
	return parseFormats(m), nil
}

// parseFormats 兼容对象数组 [{ext,filesize}] 与字符串数组 ["epub", ...]。
func parseFormats(m map[string]any) []ZFormat {
	raw, _ := m["formats"].([]any)
	formats := make([]ZFormat, 0, len(raw))
	for _, item := range raw {
		switch t := item.(type) {
		case string:
			if ext := normalizeExt(t); ext != "" {
				formats = append(formats, ZFormat{Ext: ext})
			}
		case map[string]any:
			ext := normalizeExt(objString(t, "ext", "extension", "format"))
			if ext == "" {
				continue
			}
			formats = append(formats, ZFormat{Ext: ext, FileSize: int64(objFloat(t, "filesize", "fileSize", "size"))})
		}
	}
	return dedupeFormats(formats)
}

// ResolveExtFormat 多格式未指定时的默认选择优先级（FR-4.2：epub > pdf > mobi > azw3 > 其他）。
var ResolveExtPriority = []string{"epub", "pdf", "mobi", "azw3"}

// PickExt 按优先级从可用格式中选一个；空列表返回 ""。
func PickExt(formats []ZFormat) string {
	if len(formats) == 0 {
		return ""
	}
	avail := make(map[string]ZFormat, len(formats))
	for _, f := range formats {
		avail[f.Ext] = f
	}
	for _, p := range ResolveExtPriority {
		if f, ok := avail[p]; ok {
			return f.Ext
		}
	}
	return formats[0].Ext
}

func urlPathEscape(s string) string {
	return url.PathEscape(s)
}
