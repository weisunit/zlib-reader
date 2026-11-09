// s-library: eapi 搜索请求构造与防御性解析（FR-3.2~3.4）。
package zlibrary

import (
	"context"
	"net/url"
	"strconv"
)

// SearchOrder 排序枚举。
const (
	OrderBestMatch = "bestmatch"
	OrderNewest    = "newest"
	OrderRating    = "rating"
)

// SearchQuery 搜索参数（字段与 §4 契约一致）。
type SearchQuery struct {
	Message    string
	Languages  []string
	Extensions []string
	YearFrom   *int
	YearTo     *int
	Order      string // bestmatch|newest|rating，默认 bestmatch
	Page       int
	Limit      int
}

// Search 代理 eapi /eapi/book/search。
func (c *Client) Search(ctx context.Context, q SearchQuery) (*ZSearchResult, error) {
	form := url.Values{}
	form.Set("message", q.Message)
	order := q.Order
	if order == "" {
		order = OrderBestMatch
	}
	form.Set("order", order)
	page := q.Page
	if page <= 0 {
		page = 1
	}
	form.Set("page", strconv.Itoa(page))
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	form.Set("limit", strconv.Itoa(limit))
	for _, l := range q.Languages {
		if l = trimSpace(l); l != "" {
			form.Add("languages[]", l)
		}
	}
	for _, e := range q.Extensions {
		if e = normalizeExt(e); e != "" {
			form.Add("extensions[]", e)
		}
	}
	if q.YearFrom != nil && *q.YearFrom > 0 {
		form.Set("yearFrom", strconv.Itoa(*q.YearFrom))
	}
	if q.YearTo != nil && *q.YearTo > 0 {
		form.Set("yearTo", strconv.Itoa(*q.YearTo))
	}

	m, err := c.postForm(ctx, "/eapi/book/search", form)
	if err != nil {
		return nil, err
	}

	result := &ZSearchResult{
		Total: objInt(m, "total", "totalCount"),
		Page:  page,
		Limit: limit,
	}
	if v := objInt(m, "page"); v > 0 {
		result.Page = v
	}
	if v := objInt(m, "limit"); v > 0 {
		result.Limit = v
	}
	rawBooks, _ := m["books"].([]any)
	for _, rb := range rawBooks {
		bookMap, ok := rb.(map[string]any)
		if !ok {
			continue
		}
		b := parseBook(bookMap)
		if b.ID == "" || b.Hash == "" {
			continue // 防御：缺复合主键的条目跳过
		}
		result.Books = append(result.Books, b)
	}
	return result, nil
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
