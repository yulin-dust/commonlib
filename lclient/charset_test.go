package lclient

import (
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// decodeToUTF8 应把 GBK 字节按 Content-Type charset 正确转成 UTF-8；UTF-8 原样透传。
func TestDecodeToUTF8(t *testing.T) {
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("高考准考证"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(decodeToUTF8(gbk, "text/html; charset=gbk")); got != "高考准考证" {
		t.Errorf("GBK 解码失败，得到 %q", got)
	}
	// 已是 UTF-8（声明 charset=utf-8）应原样返回。
	if got := string(decodeToUTF8([]byte("已是utf8"), "text/html; charset=utf-8")); got != "已是utf8" {
		t.Errorf("UTF-8 透传失败，得到 %q", got)
	}
	// 空 body 不报错。
	if got := decodeToUTF8(nil, ""); got != nil {
		t.Errorf("空 body 应原样返回，得到 %v", got)
	}
}
