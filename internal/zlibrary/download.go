// s-library: 流式下载（个人域名）+ 进度回调 + 魔数校验（FR-4.3 / US-M1-05）。
package zlibrary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ProgressFunc 进度回调：done/total 字节；total<=0 表示未知。
type ProgressFunc func(done, total int64)

// downloadChunkSize 流式拷贝块大小。
const downloadChunkSize = 64 * 1024

// Download 从个人下载地址（详情 dl 字段）流式下载到 w，周期性回调进度。
// 个人域名不做轮换；带 remix 头与 UA。ctx 取消时返回 ctx 错误。
func (c *Client) Download(ctx context.Context, downloadURL string, w io.Writer, progress ProgressFunc) error {
	if strings.TrimSpace(downloadURL) == "" {
		return NewZError(CodeNotFound, "缺少下载地址（dl 链接为空）", nil)
	}
	if _, err := url.Parse(downloadURL); err != nil {
		return NewZError(CodeNotFound, "下载地址非法", err)
	}

	c.mu.Lock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		c.mu.Unlock()
		return NewZError(CodeUpstream, "构造下载请求失败", err)
	}
	c.setAuthOnRequest(req)
	hc := c.downloadHTTP
	c.mu.Unlock()

	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return NewZError(CodeUpstream, "下载已取消", ctx.Err())
		}
		return NewZError(CodeDomainUnreachable, "下载域名不可达", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// continue below
	case http.StatusUnauthorized:
		return NewZError(CodeAuthRequired, "下载需要有效登录态", nil)
	case http.StatusForbidden, http.StatusServiceUnavailable:
		if isCloudflareChallenge(resp) {
			return NewZError(CodeBlocked, "下载被 Cloudflare 拦截", nil)
		}
		return NewZError(CodeBlocked, "下载被上游拒绝", nil)
	case http.StatusNotFound, http.StatusGone:
		return NewZError(CodeNotFound, "文件已不可下载", nil)
	case http.StatusTooManyRequests:
		return NewZError(CodeRateLimited, "下载过于频繁，被上游限流", nil)
	default:
		if resp.StatusCode >= 500 {
			return NewZError(CodeDomainUnreachable, "下载服务错误", nil)
		}
		return NewZError(CodeUpstream, fmt.Sprintf("下载返回异常状态 %d", resp.StatusCode), nil)
	}

	total := resp.ContentLength
	var done int64
	buf := make([]byte, downloadChunkSize)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return NewZError(CodeUpstream, "写入下载内容失败", werr)
			}
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return NewZError(CodeUpstream, "下载已取消", ctx.Err())
			}
			if errors.Is(rerr, io.ErrUnexpectedEOF) {
				// Content-Length 已知但流提前结束 → 数据不完整（非域名问题）
				return NewZError(CodeUpstream, fmt.Sprintf("下载数据不完整（%d/%d 字节）", done, total), rerr)
			}
			return NewZError(CodeDomainUnreachable, "下载中断", rerr)
		}
	}
	// 短包检测：Content-Length 已知但数据不足
	if total > 0 && done != total {
		return NewZError(CodeUpstream, fmt.Sprintf("下载数据不完整（%d/%d 字节）", done, total), nil)
	}
	return nil
}

// ValidateMagic 魔数校验：EPUB/CBZ/ZIP=PK、PDF=%PDF、MOBI/AZW3=BOOKMOBI(offset 60)。
// txt/html 等纯文本无魔数，直接通过。返回 nil 表示校验通过。
func ValidateMagic(ext string, r io.Reader) error {
	ext = normalizeExt(ext)
	need := 0
	kind := ""
	switch ext {
	case "epub", "cbz", "zip":
		need, kind = 4, "zip"
	case "pdf":
		need, kind = 5, "pdf"
	case "mobi", "azw3", "prc":
		need, kind = 68, "palm"
	default:
		return nil
	}
	head := make([]byte, need)
	n, err := io.ReadFull(r, head)
	if err != nil && n < need {
		// 文件比魔数还短：仅当魔数本身可完整比较时判失败
		if kind == "zip" && n >= 2 && head[0] == 'P' && head[1] == 'K' {
			return nil
		}
		if kind == "pdf" && n >= 4 && string(head[:4]) == "%PDF" {
			return nil
		}
		return fmt.Errorf("文件过短，无法通过 %s 魔数校验", ext)
	}
	switch kind {
	case "zip":
		if head[0] != 'P' || head[1] != 'K' {
			return fmt.Errorf("魔数校验失败：期望 PK(zip/epub) 开头")
		}
	case "pdf":
		if string(head[:4]) != "%PDF" {
			return fmt.Errorf("魔数校验失败：期望 %%PDF 开头")
		}
	case "palm":
		if string(head[60:68]) != "BOOKMOBI" && string(head[60:68]) != "TEXtREAd" {
			// BOOKMOBI 覆盖 mobi/azw3(kf8)；TEXtREAd 为旧版 PalmDOC
			return fmt.Errorf("魔数校验失败：期望 BOOKMOBI(mobi) 头")
		}
	}
	return nil
}
