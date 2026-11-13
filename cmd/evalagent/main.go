// Command evalagent 跑 Agent 评测集（PRD §4.9）。
//
// 会真实调用配置的供应商并消耗 token：
//
//	SERIALENS_AI_API_KEY=sk-... go run ./cmd/evalagent
//	go run ./cmd/evalagent -cases internal/agent/eval/cases.jsonl -filter aa55 -max-attempts 3
//
// 输出 JSON 报告（首试通过率 / 3 轮内通过率 / 平均 token / 平均耗时），
// CI 可用 make eval-agent 拿到报告做回归门槛。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"seriallens/internal/agent/eval"
	"seriallens/internal/llm"
)

func main() {
	casesPath := flag.String("cases", "internal/agent/eval/cases.jsonl", "用例集路径")
	filter := flag.String("filter", "", "按名字子串过滤用例")
	maxAttempts := flag.Int("max-attempts", 3, "自修复轮次上限")
	timeoutSec := flag.Int("timeout", 180, "单用例超时（秒）")
	out := flag.String("out", "", "报告输出文件（缺省打到 stdout）")
	flag.Parse()

	st, err := llm.NewStore("").Load()
	if err != nil {
		fatal("读取 AI 配置失败: %v", err)
	}
	if st.APIKey == "" {
		fatal("未配置 API Key（设置 SERIALENS_AI_API_KEY 或在应用里保存过）")
	}
	st = st.Resolved()

	cases, err := eval.LoadCases(*casesPath)
	if err != nil {
		fatal("读取用例失败: %v", err)
	}
	if *filter != "" {
		var kept []eval.Case
		for _, c := range cases {
			if strings.Contains(strings.ToLower(c.Name), strings.ToLower(*filter)) {
				kept = append(kept, c)
			}
		}
		cases = kept
	}
	if len(cases) == 0 {
		fatal("没有可用用例")
	}

	results := make([]eval.Result, 0, len(cases))
	for i, c := range cases {
		fmt.Fprintf(os.Stderr, "[%d/%d] %s …\n", i+1, len(cases), c.Name)
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(*timeoutSec)*time.Second)
		r := eval.RunCase(ctx, c, eval.Options{
			Settings: st, MaxAttempts: *maxAttempts,
			Timeout: time.Duration(*timeoutSec) * time.Second,
		})
		cancel()
		status := "PASS"
		if !r.Pass {
			status = "FAIL"
		}
		fmt.Fprintf(os.Stderr, "      %s（%d 轮，%d tokens，%.1fs）%s\n", status, r.Attempts, r.Tokens,
			float64(r.ElapsedMS)/1000, r.Error)
		results = append(results, r)
	}

	rep := eval.Summarize(results)
	raw, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fatal("%v", err)
	}
	if *out != "" {
		if err := os.WriteFile(*out, raw, 0o644); err != nil {
			fatal("%v", err)
		}
		fmt.Fprintf(os.Stderr, "报告已写入 %s\n", *out)
	}
	fmt.Println(string(raw))
}

func fatal(f string, a ...any) {
	fmt.Fprintf(os.Stderr, "evalagent: "+f+"\n", a...)
	os.Exit(1)
}
