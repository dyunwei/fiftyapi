package i18n

import "testing"

func TestLanguage(t *testing.T) {
	for _, tc := range []struct{ header, want string }{
		{"", "zh-CN"}, {"fr-FR", "zh-CN"}, {"en-US,en;q=0.9", "en"},
		{"zh-TW,en;q=0.9", "zh-CN"}, {"zh;q=0.2,en;q=0.8", "en"},
		{"en;q=0,zh;q=0.5", "zh-CN"}, {"en;q=bad", "zh-CN"}, {"en;q=2", "zh-CN"},
	} {
		if got := Language(tc.header); got != tc.want {
			t.Errorf("Language(%q) = %q; want %q", tc.header, got, tc.want)
		}
	}
}
func TestTranslate(t *testing.T) {
	for _, tc := range []struct{ language, message, want string }{
		{"en", "账号或密码错误", "Incorrect account or password"},
		{"zh-CN", "账号或密码错误", "账号或密码错误"},
		{"en", "筛选参数无效：account_id", "Invalid filter:account_id"},
		{"en", "S3 操作失败（HTTP 403），请检查区域、存储桶和读写删除权限", "S3 operation failed (HTTP 403). Check region, bucket and read/write/delete permissions."},
		{"en", "external upstream detail", "external upstream detail"},
	} {
		if got := Translate(tc.language, tc.message); got != tc.want {
			t.Errorf("Translate(%q, %q) = %q; want %q", tc.language, tc.message, got, tc.want)
		}
	}
}
