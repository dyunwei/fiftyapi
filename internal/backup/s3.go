package backup

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func hashHex(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func mac(key []byte, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(value))
	return h.Sum(nil)
}
func uriEncode(path string) string {
	var out strings.Builder
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.~/", rune(c)) {
			out.WriteByte(c)
		} else {
			fmt.Fprintf(&out, "%%%02X", c)
		}
	}
	return out.String()
}
func sign(req *http.Request, c Config, payload string, now time.Time) {
	stamp := now.UTC().Format("20060102T150405Z")
	day := stamp[:8]
	req.Header.Set("X-Amz-Date", stamp)
	req.Header.Set("X-Amz-Content-Sha256", payload)
	headers := "host:" + req.URL.Host + "\nx-amz-content-sha256:" + payload + "\nx-amz-date:" + stamp + "\n"
	signed := "host;x-amz-content-sha256;x-amz-date"
	canonical := req.Method + "\n" + uriEncode(req.URL.Path) + "\n" + req.URL.RawQuery + "\n" + headers + "\n" + signed + "\n" + payload
	scope := day + "/" + c.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + hashHex([]byte(canonical))
	key := mac(mac(mac(mac([]byte("AWS4"+c.SecretKey), day), c.Region), "s3"), "aws4_request")
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+c.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+hex.EncodeToString(mac(key, toSign)))
}
func (s *Service) object(ctx context.Context, c Config, method, key string, body io.Reader, size int64, payload string) (*http.Response, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil {
		return nil, errors.New("存储端点无效")
	}
	if c.PathStyle {
		u.Path = "/" + c.Bucket + "/" + key
	} else {
		u.Host = c.Bucket + "." + u.Host
		u.Path = "/" + key
	}
	u.RawPath = uriEncode(u.Path)
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, errors.New("无法创建存储请求")
	}
	req.ContentLength = size
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	sign(req, c, payload, time.Now())
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, errors.New("无法连接 S3 存储，请检查端点、网络及证书")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		if method == http.MethodDelete && resp.StatusCode == 404 {
			return nil, nil
		}
		return nil, fmt.Errorf("S3 操作失败（HTTP %d），请检查区域、存储桶和读写删除权限", resp.StatusCode)
	}
	return resp, nil
}
func (s *Service) deleteObject(ctx context.Context, c Config, key string) error {
	resp, err := s.object(ctx, c, http.MethodDelete, key, nil, 0, hashHex(nil))
	if resp != nil {
		resp.Body.Close()
	}
	return err
}
func (s *Service) Test(ctx context.Context, c Config) error {
	c, err := s.ResolveConfig(ctx, c)
	if err != nil {
		return err
	}
	task, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	key := c.Prefix + ".connection-test-" + newID()
	body := "fiftyapi storage connection test\n"
	resp, err := s.object(task, c, http.MethodPut, key, strings.NewReader(body), int64(len(body)), hashHex([]byte(body)))
	if err != nil {
		return err
	}
	resp.Body.Close()
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	if err = s.deleteObject(cleanup, c, key); err != nil {
		return errors.New("连接成功但测试文件清理失败，请检查删除权限；测试文件：" + key)
	}
	return nil
}
