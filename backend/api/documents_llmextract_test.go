package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"llm-knowledge/agent"
	"llm-knowledge/db"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestLLMExtract_PromptViaStdinAndProtocolBin 覆盖 Task 8 留下的缺口:
// api/documents.go 的 PDF 逐页转 Markdown 是那次改动里唯一既无测试、又难自动化的路径。
//
// 它钉住三件事:
//
//  1. **D2**:prompt 从 stdin 进,不在 argv 里。放 argv 会对 `ps` 可见(而 prompt 里含
//     用户文档路径),且受 ARG_MAX 限制
//  2. **用的是 Protocol 给的二进制**,而不是硬编码的 "claude"。这条其实已被
//     agent/invariant_test.go 的规则 1/2 覆盖(Task 8 摘掉了 documents.go 的豁免),
//     但那是源码级扫描;这里从运行时再证一次 —— 假二进制被真的执行了
//  3. **D3**:model 经 OnceArgs 传入,claude 侧出现 `--model sonnet`
//
// 需要 poppler(pdfinfo/pdftoppm),缺失时跳过 —— 那是环境依赖,不是被测行为。
func TestLLMExtract_PromptViaStdinAndProtocolBin(t *testing.T) {
	for _, bin := range []string{"pdfinfo", "pdftoppm"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("需要 %s(poppler),未安装: %v", bin, err)
		}
	}
	// ---- DB ----
	testDB, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	db.DB = testDB
	if err := testDB.AutoMigrate(&db.Document{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	// ---- 目录结构:userDir/<rawRelPath>/paper.pdf ----
	dataDir := t.TempDir()
	userDir := filepath.Join(dataDir, "users", "1")
	rawRelPath := filepath.Join("raw", "papers", "sample")
	pdfDir := filepath.Join(userDir, rawRelPath)
	if err := os.MkdirAll(pdfDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sample, err := os.ReadFile(filepath.Join("..", "ingest", "testdata", "sample.pdf"))
	if err != nil {
		t.Skipf("仓库里没有 backend/ingest/testdata/sample.pdf: %v", err)
	}

	// 测试用的 PDF **必须至少 10 页**,否则这个用例会在逐页循环里静默跳过。
	//
	// 原因是 handler 与 pdftoppm 对页码补零的约定不一致(既有缺陷,不属 Plan 2,
	// 已向维护者报告):pdftoppm 按**总页数**决定补零宽度 —— 10 页的 PDF 产出
	// page-01.png,而 1 页的产出 page-1.png;handler 则硬编码了
	// fmt.Sprintf("page-%02d.png", i)。实测对照:
	//
	//	1 页的 sample.pdf  -> pdftoppm 产出 page-1.png  -> handler 找 page-01.png 失败
	//	10 页的 PDF        -> pdftoppm 产出 page-01.png -> handler 命中
	//
	// 失败时它 `continue` 跳过该页(而且是在写入页眉之前),最后仍返回 200 与
	// "PDF extracted with LLM successfully",产物 paper.md 却是空的 ——
	// 一个静默失败。所以这里用 pdfunite 把单页样本拼成 10 页,走真实路径。
	// 修好补零问题之后,这段拼接可以删掉。
	united := filepath.Join(t.TempDir(), "multipage.pdf")
	if _, err := exec.LookPath("pdfunite"); err != nil {
		t.Skipf("需要 pdfunite(poppler)把单页样本拼成 10 页,未安装: %v", err)
	}
	src := filepath.Join(t.TempDir(), "sample.pdf")
	if err := os.WriteFile(src, sample, 0o644); err != nil {
		t.Fatalf("write sample copy: %v", err)
	}
	// 注意:pdfunite 的最后一个位置参数是**输出**。绝不能少给 —— 少给时它会把
	// 最后一个输入当成输出文件覆写掉(本次开发中就这么写坏过仓库里的 sample.pdf)。
	uniteArgs := make([]string, 0, 12)
	for i := 0; i < 10; i++ {
		uniteArgs = append(uniteArgs, src)
	}
	uniteArgs = append(uniteArgs, united)
	if out, err := exec.Command("pdfunite", uniteArgs...).CombinedOutput(); err != nil {
		t.Skipf("pdfunite 拼接失败: %v (%s)", err, out)
	}
	pdfBytes, err := os.ReadFile(united)
	if err != nil {
		t.Fatalf("read united pdf: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pdfDir, "paper.pdf"), pdfBytes, 0o644); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	// RawPath 带 users/<id>/ 前缀,StripUserPrefix 会剥掉它
	doc := db.Document{
		Title:      "LLMExtract stdin test",
		Slug:       "llmextract-stdin-test",
		RawPath:    "users/1/" + filepath.ToSlash(rawRelPath),
		SourceType: "pdf",
		Status:     "inbox",
		UserID:     1,
	}
	if err := db.DB.Create(&doc).Error; err != nil {
		t.Fatalf("create doc: %v", err)
	}

	// ---- 假 claude:把 argv 与 stdin 分别落到文件里 ----
	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	stdinFile := filepath.Join(t.TempDir(), "stdin.txt")
	binDir := t.TempDir()
	fakeBin := filepath.Join(binDir, "claude")
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > %q
cat > %q
echo "FAKE-MARKDOWN-OUTPUT"
`, argvFile, stdinFile)
	if err := os.WriteFile(fakeBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}

	agent.Init(agent.ResolverOptions{ClaudeBin: fakeBin})
	t.Cleanup(func() { agent.Init(agent.ResolverOptions{}) })

	// ---- 调用 handler:只处理第 1 页,保证假二进制恰好被调用一次 ----
	e := setupTestEcho()
	h := &DocHandler{DataDir: dataDir}
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/documents/%d/llm-extract?start_page=1&end_page=1", doc.ID), nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(fmt.Sprintf("%d", doc.ID))
	c.Set("userId", uint(1))
	c.Set("userDir", userDir)

	if err := h.LLMExtract(c); err != nil {
		t.Fatalf("LLMExtract 返回错误: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// ---- 断言 1:假二进制确实被 spawn 了(否则下面全是空过)----
	rawArgv, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("假二进制没有被执行(argv 文件不存在)—— 说明这条路径没走到 spawn: %v", err)
	}
	rawStdin, err := os.ReadFile(stdinFile)
	if err != nil {
		t.Fatalf("假二进制的 stdin 没有被读取: %v", err)
	}
	argv := string(rawArgv)
	stdin := string(rawStdin)

	// ---- 断言 2(D2):prompt 在 stdin 里,不在 argv 里 ----
	if !strings.Contains(stdin, "读取图片") {
		t.Errorf("prompt 没有从 stdin 进去。stdin = %q", stdin)
	}
	if !strings.Contains(stdin, "page-01.png") {
		t.Errorf("stdin 里的 prompt 没有指向第 1 页的图片(说明页码没被正确代入)。stdin = %q", stdin)
	}
	if strings.Contains(argv, "读取图片") {
		t.Errorf("prompt 出现在了 argv 里(D2 违背:对 ps 可见、受 ARG_MAX 限制)。argv =\n%s", argv)
	}

	// ---- 断言 3(D3):--model sonnet 由 OnceArgs 加上 ----
	if !strings.Contains(argv, "--model\nsonnet") && !strings.Contains(argv, "--model sonnet") {
		t.Errorf("argv 里没有 --model sonnet(D3:claude 侧必须带 model hint)。argv =\n%s", argv)
	}
	// 安全语义不能因为改了构造方式而丢失
	for _, want := range []string{"--disallowedTools", "--dangerously-skip-permissions"} {
		if !strings.Contains(argv, want) {
			t.Errorf("argv 里缺 %q —— 改走 OnceArgs 后安全旗标丢了。argv =\n%s", want, argv)
		}
	}

	// ---- 断言 4:假二进制的输出被写进了产物,证明整条链路是通的 ----
	md, err := os.ReadFile(filepath.Join(pdfDir, "paper.md"))
	if err != nil {
		t.Fatalf("没有生成 paper.md: %v", err)
	}
	if !strings.Contains(string(md), "FAKE-MARKDOWN-OUTPUT") {
		t.Errorf("paper.md 里没有假二进制的输出,说明 stdout 没被采集。内容 = %q", string(md))
	}
}
