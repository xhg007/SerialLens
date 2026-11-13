package llm

import "testing"

// 数据外发告知的「一次确认、永久记住」：Save 不能把 notice_ack 顺手清掉，
// 否则用户每改一次模型就被弹一次窗。
func TestNoticeAckPersistsAcrossSave(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)

	if s, _ := st.Load(); s.NoticeAck {
		t.Fatal("fresh store should not be acknowledged")
	}
	if err := st.Save(Settings{Provider: "custom", BaseURL: "https://api.example.com/v1", Model: "m", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkNoticeAck(); err != nil {
		t.Fatal(err)
	}

	// 再保存一次（用户改模型），ack 必须保留
	if err := st.Save(Settings{Provider: "custom", BaseURL: "https://api.example.com/v1", Model: "m2", APIKey: "k"}); err != nil {
		t.Fatal(err)
	}
	s, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !s.NoticeAck {
		t.Fatal("notice_ack lost after Save")
	}
	if s.Model != "m2" {
		t.Fatalf("model = %q", s.Model)
	}
	if !s.Masked()["notice_ack"].(bool) {
		t.Fatal("masked view should expose notice_ack")
	}
}

func TestIsLocalEndpoint(t *testing.T) {
	local := []string{
		"http://127.0.0.1:11434/v1",
		"http://localhost:11435/v1",
		"http://[::1]:8080/v1",
		"127.0.0.1:9200/v1",
	}
	for _, u := range local {
		if !IsLocalEndpoint(u) {
			t.Fatalf("%s should be local", u)
		}
	}
	remote := []string{
		"https://api.deepseek.com/v1",
		"https://dashscope.aliyuncs.com/compatible-mode/v1",
		"",
	}
	for _, u := range remote {
		if IsLocalEndpoint(u) {
			t.Fatalf("%q should not be local", u)
		}
	}

	if got := EndpointHost("https://api.deepseek.com/v1"); got != "api.deepseek.com" {
		t.Fatalf("host = %q", got)
	}
}
