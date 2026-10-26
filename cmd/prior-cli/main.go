// Command prior-cli runs the knowledge base's pipeline without a window:
// it surveys a folder of documents, ingests it into PostgreSQL, analyzes a
// new document against what is stored, and reports the state of the
// connections. The window app calls the same packages.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"prior/agent"
	"prior/embed"
	"prior/ingest"
	"prior/llm"
	"prior/retrieval"
	"prior/store"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "survey":
		survey(os.Args[2:])
	case "ingest":
		ingestCmd(os.Args[2:])
	case "status":
		statusCmd(os.Args[2:])
	case "analyze":
		analyzeCmd(os.Args[2:])
	case "modules":
		modulesCmd(os.Args[2:])
	case "points":
		pointsCmd(os.Args[2:])
	case "dates":
		datesCmd(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "prior-cli: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `prior-cli: 先例的命令行

用法:
  prior-cli survey <目录>    解析目录下的文档，报告格式、日期来源、切块与问题
  prior-cli ingest <目录>    把目录下的文档入库（解析 → 切块 → embedding → 入库）
      -manifest <文件>       下载清单 CSV，用于补齐模块名与链接
      -ledger                另外抽取功能点与基线规则（默认对最近 12 份）
      -ledger-limit <n>      抽取台账的文档数
      -dsn <dsn>             PostgreSQL 连接串
      -ollama <url>          Ollama 地址
      -embed <模型>          embedding 模型
      -protocol <名称>       语言模型协议：ollama / openai / anthropic（默认 ollama）
      -endpoint <url>        语言模型端点，如 https://api.deepseek.com/v1（ollama 协议用 -ollama）
      -llm-key <key>         语言模型的 API Key（也可用 PRIOR_LLM_KEY）
      -llm <模型>            语言模型（默认 ollama 下为 qwen3:latest）
  prior-cli analyze <文档>   用知识库评审一份新文档（流程 B：检索 → 四类发现 → 变更账本）
      -dsn / -ollama / -embed / -protocol / -endpoint / -llm-key / -llm  同上
      -no-rerank             跳过模型重排（更快，但语义排序变差）
      -out <文件>            把报告写成 Markdown
  prior-cli modules [目录]   重判每份文档所属的系统/模块（流程 1 的模块归属）
      -apply                 写回数据库（默认只预览）
      -protocol / -endpoint / -llm-key / -llm  语言模型，同上
      -limit N               只处理前 N 份
  prior-cli points           功能点台账：列出功能点与待确认的重复候选
      -merge FROM TO         把 FROM 功能点的历史并入 TO（人工确认后的合并）
      -candidates            只看有候选或人工合并记录的功能点
  prior-cli dates [目录]     文档成稿日期：列出编号、日期（来源·置信度）与标题
      -unknown               只列日期未知的文档
      -set <编号> <日期>      人工补录成稿日期（来源记 manual，该文档的账本日期随之移动）
      -rescan [目录]          重新解析每份文件，只更新日期与版本序（不动向量与账本）
  prior-cli status           连接自检与知识库统计
  prior-cli help             显示本说明
`)
}

// survey reads every document of a folder and reports what the pipeline
// would make of it: the facts a corpus is judged by before it is indexed.
func survey(args []string) {
	fs := flag.NewFlagSet("survey", flag.ExitOnError)
	limit := fs.Int("limit", 0, "只解析前 N 个文档，0 为全部")
	verbose := fs.Bool("v", false, "逐个文档打印")
	rest, err := parseArgs(fs, args)
	if err != nil {
		os.Exit(2)
	}
	dir := firstOr(rest, "")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "survey: 需要一个目录")
		os.Exit(2)
	}

	paths, err := documents(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "survey: %v\n", err)
		os.Exit(1)
	}
	if *limit > 0 && len(paths) > *limit {
		paths = paths[:*limit]
	}

	var (
		byFormat = map[string]int{}
		bySource = map[string]int{}
		byType   = map[string]int{}
		failed   []string
		warned   []string
		chunks   int
		chars    int
		docs     []*ingest.Doc
		started  = time.Now()
	)
	for _, path := range paths {
		doc, err := ingest.Parse(path)
		if err != nil {
			failed = append(failed, filepath.Base(path)+": "+err.Error())
			continue
		}
		docs = append(docs, doc)
		byFormat[doc.Format]++
		bySource[doc.Meta.DateSource]++
		byType[doc.Meta.DocType]++
		if len(doc.Warnings) > 0 {
			warned = append(warned, filepath.Base(path))
		}
		cs := ingest.Chunking(doc.Blocks)
		chunks += len(cs)
		chars += doc.Chars
		if *verbose {
			fmt.Printf("  %-64s %-5s %7d 字 %3d 块 %s(%s)\n",
				truncate(filepath.Base(path), 64), doc.Format, doc.Chars, len(cs),
				doc.Meta.Date.Format("2006-01-02"), doc.Meta.DateSource)
		}
	}
	fmt.Printf("目录        %s\n", dir)
	fmt.Printf("文档        %d 个（失败 %d）\n", len(docs), len(failed))
	fmt.Printf("格式        %s\n", counts(byFormat))
	fmt.Printf("文档类型    %s\n", counts(byType))
	fmt.Printf("日期来源    %s\n", counts(bySource))
	fmt.Printf("正文        %d 字，%d 块（平均 %.0f 字/块）\n", chars, chunks, average(chars, chunks))
	fmt.Printf("解析告警    %d 个\n", len(warned))
	fmt.Printf("耗时        %s\n", time.Since(started).Round(time.Millisecond))
	for _, f := range failed {
		fmt.Printf("失败        %s\n", f)
	}
	if len(docs) > 0 {
		ingest.SortByDate(docs)
		fmt.Println("最早/最新    ", docs[len(docs)-1].Meta.Date.Format("2006-01-02"), "/", docs[0].Meta.Date.Format("2006-01-02"))
		fmt.Println("最近五篇    ")
		for i, d := range docs {
			if i == 5 {
				break
			}
			fmt.Printf("            %s  %s\n", d.Meta.Date.Format("2006-01-02"), truncate(d.Title, 60))
		}
	}
}

// modelFlags are the language model's own settings, which every command
// that calls one takes: the API it answers over, where it answers, the key
// it wants, and the model itself.
type modelFlags struct {
	protocol *string
	endpoint *string
	key      *string
	model    *string
}

// addModelFlags adds them to a command's flags.
func addModelFlags(fs *flag.FlagSet) modelFlags {
	return modelFlags{
		protocol: fs.String("protocol", envOr("PRIOR_LLM_PROTOCOL", llm.ProtocolOllama),
			"语言模型协议：ollama / openai / anthropic"),
		endpoint: fs.String("endpoint", envOr("PRIOR_LLM_ENDPOINT", ""),
			"语言模型端点，如 https://api.deepseek.com/v1（ollama 协议用 -ollama）"),
		key:   fs.String("llm-key", envOr("PRIOR_LLM_KEY", ""), "provider 的 API Key"),
		model: fs.String("llm", envOr("PRIOR_LLM", llm.DefaultModel(llm.ProtocolOllama)), "语言模型"),
	}
}

// client is the language model these flags name, ready for a run: an Ollama
// model answers at the Ollama server, and the run's own context window and
// timeout bound its calls.
func (f modelFlags) client(ollamaURL string, numCtx, timeoutSec int) *llm.Client {
	protocol, endpoint := *f.protocol, *f.endpoint
	if protocol == "" || protocol == llm.ProtocolOllama {
		protocol, endpoint = llm.ProtocolOllama, ollamaURL
	}
	gen := llm.New(llm.Config{
		Protocol: protocol,
		Endpoint: endpoint,
		APIKey:   *f.key,
		Model:    *f.model,
	})
	gen.Configure(numCtx, timeoutSec)
	return gen
}

// where is the language model as a line of output: the protocol, the model,
// and the endpoint it answers at.
func (f modelFlags) where(ollamaURL string) string {
	gen := f.client(ollamaURL, 0, 0)
	return gen.Protocol() + " · " + gen.Model() + " · " + gen.Endpoint()
}

// ingestCmd runs the ingest pipeline over a folder.
func ingestCmd(args []string) {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	dsn := fs.String("dsn", envOr("PRIOR_DSN", "postgres://postgres:postgres@127.0.0.1:5433/prior"), "PostgreSQL 连接串")
	ollama := fs.String("ollama", envOr("PRIOR_OLLAMA", "http://127.0.0.1:11434"), "Ollama 地址")
	embedModel := fs.String("embed", envOr("PRIOR_EMBED", "bge-m3:latest"), "embedding 模型")
	mf := addModelFlags(fs)
	manifest := fs.String("manifest", "", "下载清单 CSV")
	ledger := fs.Bool("ledger", false, "抽取功能点与基线规则")
	ledgerLimit := fs.Int("ledger-limit", 12, "抽取台账的文档数")
	temperature := fs.Float64("temp", agent.DefaultTemperature, "模型温度（0.1 为默认）")
	numCtx := fs.Int("num-ctx", llm.DefaultNumCtx, "模型上下文窗口")
	timeoutSec := fs.Int("timeout-sec", 600, "单次模型调用的超时（秒）")
	quiet := fs.Bool("q", false, "只打印进度行，不打印每份文档")
	force := fs.Bool("force", false, "重新读取已在库中的文档（解析规则改动后用）")
	rest, err := parseArgs(fs, args)
	if err != nil {
		os.Exit(2)
	}
	dir := firstOr(rest, "")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "ingest: 需要一个目录")
		os.Exit(2)
	}
	if *manifest == "" {
		candidate := filepath.Join(dir, "下载清单.csv")
		if _, err := os.Stat(candidate); err == nil {
			*manifest = candidate
		}
	}

	ctx := context.Background()
	st, err := store.Open(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	em := embed.New(*ollama, *embedModel, mf.client(*ollama, *numCtx, *timeoutSec))
	models, err := em.Tags(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
	if !llm.HasModel(models, *embedModel, llm.ProtocolOllama) {
		fmt.Fprintf(os.Stderr, "ingest: embedding 模型 %s 不在 %v\n", *embedModel, models)
		os.Exit(1)
	}
	// The language model is called for the ledger alone, and answers where
	// its own protocol says.
	if *ledger {
		listed, err := em.LLM.Models(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
			os.Exit(1)
		}
		if !llm.HasModel(listed, *mf.model, em.LLM.Protocol()) {
			fmt.Fprintf(os.Stderr, "ingest: 语言模型 %s 不在 %v\n", *mf.model, listed)
			os.Exit(1)
		}
	}
	fmt.Printf("知识库      %s（pgvector %v）\n", *dsn, st.HasVector())
	fmt.Printf("Ollama      %s（embedding %s）\n", *ollama, *embedModel)
	fmt.Printf("语言模型    %s\n", mf.where(*ollama))

	started := time.Now()
	last := ""
	res, err := agent.Ingest(ctx, st, em, agent.IngestOptions{
		Root: dir, Manifest: *manifest, Ledger: *ledger, LedgerLimit: *ledgerLimit,
		ParserVersion: ingest.ParserVersion, EmbedModel: *embedModel, LLMModel: *mf.model,
		Temperature: *temperature, Force: *force,
		Progress: func(ev agent.Event) {
			if *quiet {
				line := fmt.Sprintf("%-8s %3d/%3d %s", ev.Stage, ev.Index, ev.Total, truncate(ev.File, 60))
				if line != last {
					fmt.Println("  ", strings.TrimSpace(line))
					last = line
				}
				return
			}
			if ev.Message != "" {
				fmt.Printf("  %-8s %s\n", ev.Stage, ev.Message)
			}
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ingest: %v\n", err)
		os.Exit(1)
	}
	docs, chunks, findings, records, _ := st.Stats(ctx)
	fmt.Printf("作业        #%d 完成 %d，跳过 %d，失败 %d，块 %d，台账 %d\n",
		res.JobID, res.Done, res.Skipped, len(res.Failures), res.Chunks, res.Ledger)
	fmt.Printf("知识库      文档 %d，块 %d，发现 %d，变更记录 %d\n", docs, chunks, findings, records)
	// The two things the archive is judged by: can it be ordered by its own
	// dates, and can it be found by system.
	dateCoverage(st, ctx, docs)
	moduleCoverage(st, ctx, docs)
	fmt.Printf("耗时        %s\n", time.Since(started).Round(time.Second))
	for _, f := range res.Failures {
		fmt.Printf("失败/跳过   %s — %s\n", truncate(f.Path, 50), truncate(f.Reason, 90))
	}
}

// analyzeCmd reviews one document against the knowledge base.
func analyzeCmd(args []string) {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	dsn := fs.String("dsn", envOr("PRIOR_DSN", "postgres://postgres:postgres@127.0.0.1:5433/prior"), "PostgreSQL 连接串")
	ollama := fs.String("ollama", envOr("PRIOR_OLLAMA", "http://127.0.0.1:11434"), "Ollama 地址")
	embedModel := fs.String("embed", envOr("PRIOR_EMBED", "bge-m3:latest"), "embedding 模型")
	mf := addModelFlags(fs)
	noRerank := fs.Bool("no-rerank", false, "跳过模型重排")
	out := fs.String("out", "", "把报告写成 Markdown 的路径")
	temperature := fs.Float64("temp", agent.DefaultTemperature, "模型温度（0.1 为默认，越低越稳定）")
	numCtx := fs.Int("num-ctx", llm.DefaultNumCtx, "模型上下文窗口")
	timeoutSec := fs.Int("timeout-sec", 600, "单次模型调用的超时（秒）")
	rest, err := parseArgs(fs, args)
	if err != nil {
		os.Exit(2)
	}
	path := firstOr(rest, "")
	if path == "" {
		fmt.Fprintln(os.Stderr, "analyze: 需要一份文档")
		os.Exit(2)
	}
	ctx := context.Background()
	doc, chunks, err := agent.ParseChunksForAnalysis(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		os.Exit(1)
	}
	st, err := store.Open(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	em := embed.New(*ollama, *embedModel, mf.client(*ollama, *numCtx, *timeoutSec))

	// The new document is stored first: the analysis cites its own chunks,
	// and the knowledge base should hold what it reviewed.
	vectors, err := em.Embed(ctx, embeddingTexts(doc, chunks))
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		os.Exit(1)
	}
	stored := make([]store.Chunk, 0, len(chunks))
	for _, c := range chunks {
		stored = append(stored, store.Chunk{Index: c.Index, Path: c.HeadingPath, Content: c.Content, Tokens: c.Tokens})
	}
	docID, err := st.SaveDocument(ctx, store.Doc{
		Title: doc.Meta.Title, SourceURI: doc.Path, Format: doc.Format, DocType: doc.Meta.DocType,
		Module: doc.Meta.Module, Author: doc.Meta.Author, Version: doc.Meta.Version, Status: "draft",
		Checksum: doc.Checksum, Chars: doc.Chars, EmbedModel: *embedModel,
		Date: doc.Meta.Date, DateSource: doc.Meta.DateSource, DateConf: doc.Meta.Confidence,
		VersionRank: ingest.VersionRank(doc.Meta.Version),
	}, stored, vectors)
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: 入库失败 %v\n", err)
		os.Exit(1)
	}
	profile := agent.ProfileDocument(doc, chunks)
	search := retrieval.Defaults()
	search.Rerank = !*noRerank

	fmt.Printf("新文档      %s\n", doc.Meta.Title)
	fmt.Printf("日期        %s（%s）  模块 %s\n", doc.Meta.Date.Format("2006-01-02"), doc.Meta.DateSource, doc.Meta.Module)
	fmt.Printf("规模        %d 字，%d 块，%d 章节\n", doc.Chars, len(chunks), profile.Stats.Sections)
	started := time.Now()

	res, err := agent.Analyze(ctx, st, em, agent.AnalyzeOptions{
		Doc: store.Doc{ID: docID, Title: doc.Meta.Title, Module: doc.Meta.Module,
			Date: doc.Meta.Date, DateSource: doc.Meta.DateSource, Status: "draft"},
		Chunks: stored, Profile: profile, Search: search,
		EmbedModel: *embedModel, LLMModel: *mf.model, Temperature: *temperature,
		Progress: func(ev agent.Event) {
			if ev.Message != "" {
				fmt.Printf("  %-4s %s\n", ev.Stage, ev.Message)
			}
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "analyze: %v\n", err)
		os.Exit(1)
	}
	counts := map[string]int{}
	for _, f := range res.Findings {
		counts[f.Type]++
	}
	fmt.Printf("运行        #%d 状态 %s\n", res.RunID, statusOfRun(res))
	fmt.Printf("检索        查询 %d，候选 %d，重排保留 %d，文档 %d 篇\n",
		res.Coverage.Queries, res.Coverage.Candidates, res.Coverage.Kept, res.Docs)
	fmt.Printf("发现        共 %d（预评审 %d / 变更信号 %d / 逻辑漏洞 %d / 不明确 %d / 一句话需求 %d）\n",
		len(res.Findings), counts["pre_review"], counts["change_signal"], counts["logic_gap"], counts["ambiguity"], counts["one_liner"])
	if pre := res.PreReview; pre.Verdict != "" {
		fmt.Printf("预评审      %s：%s\n", verdictName(pre.Verdict), pre.Summary)
	}
	fmt.Printf("变更账本    %d 条记录，%d 个功能点\n", res.Records, res.Points)
	for _, d := range res.Degraded {
		fmt.Printf("降级        %s\n", d)
	}
	fmt.Printf("耗时        %s\n", time.Since(started).Round(time.Second))
	if *out != "" {
		if err := writeReport(*out, doc, res); err != nil {
			fmt.Fprintf(os.Stderr, "analyze: 报告写入失败 %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("报告        %s\n", *out)
	}
}

func statusOfRun(res agent.AnalyzeResult) string {
	if len(res.Degraded) > 0 {
		return "degraded"
	}
	return "succeeded"
}

// embeddingTexts is what the embedder sees for a document's chunks.
func embeddingTexts(doc *ingest.Doc, chunks []ingest.Chunk) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c.EmbeddingText(doc.Title))
	}
	return out
}

// writeReport writes the review as Markdown, in the shape a product reader
// expects: what changed, what is unclear, and what to answer.
func writeReport(path string, doc *ingest.Doc, res agent.AnalyzeResult) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# 评审报告：%s\n\n", doc.Meta.Title)
	fmt.Fprintf(&b, "- 文档日期：%s（%s）\n", doc.Meta.Date.Format("2006-01-02"), doc.Meta.DateSource)
	fmt.Fprintf(&b, "- 运行：#%d　检索：查询 %d / 候选 %d / 保留 %d / 文档 %d 篇\n",
		res.RunID, res.Coverage.Queries, res.Coverage.Candidates, res.Coverage.Kept, res.Docs)
	fmt.Fprintf(&b, "- 发现：%d 条　变更记录：%d 条 / %d 个功能点\n\n", len(res.Findings), res.Records, res.Points)

	if pre := res.PreReview; pre.Verdict != "" {
		fmt.Fprintf(&b, "## 预评审结论：%s\n\n%s\n\n", verdictName(pre.Verdict), pre.Summary)
		if len(pre.Risks) > 0 {
			b.WriteString("### 最大的工程风险\n\n")
			for _, risk := range pre.Risks {
				fmt.Fprintf(&b, "- %s\n", risk)
			}
			b.WriteString("\n")
		}
		if len(pre.Asked) > 0 {
			b.WriteString("### 致产品经理的追问清单\n\n")
			for i, q := range pre.Asked {
				fmt.Fprintf(&b, "%d. %s\n", i+1, q)
			}
			b.WriteString("\n")
		}
	}
	groups := []struct{ typ, title string }{
		{"change_signal", "变更信号与影响面"},
		{"pre_review", "预评审：工程缺口"},
		{"logic_gap", "逻辑漏洞"},
		{"ambiguity", "不明确"},
		{"one_liner", "一句话需求"},
	}
	for _, group := range groups {
		var list []store.Finding
		for _, f := range res.Findings {
			if f.Type == group.typ {
				list = append(list, f)
			}
		}
		if len(list) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s（%d）\n\n", group.title, len(list))
		for i, f := range list {
			fmt.Fprintf(&b, "### %d. [%s] %s\n\n", i+1, f.Severity, f.Title)
			fmt.Fprintf(&b, "%s\n\n", f.Detail)
			if f.Span.Quote != "" {
				fmt.Fprintf(&b, "> 新文档：%s\n", f.Span.Quote)
				if len(f.Span.Path) > 0 {
					fmt.Fprintf(&b, "> （%s）\n", strings.Join(f.Span.Path, " > "))
				}
				b.WriteString("\n")
			}
			for _, e := range f.Evidence {
				fmt.Fprintf(&b, "> 历史《%s》：%s\n", e.DocTitle, e.Quote)
			}
			if f.Impact != "" {
				fmt.Fprintf(&b, "\n**影响面**：%s\n", f.Impact)
			}
			if f.Question != "" {
				fmt.Fprintf(&b, "\n**给产品的问题**：%s\n", f.Question)
			}
			if f.Fix != "" {
				fmt.Fprintf(&b, "\n**建议改写**：%s\n", f.Fix)
			}
			fmt.Fprintf(&b, "\n置信度 %.2f\n\n", f.Confidence)
		}
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return nil
}

// verdictName is a pre-review's verdict as the report writes it.
func verdictName(verdict string) string {
	switch verdict {
	case "ready":
		return "可进入排期"
	case "clarify":
		return "需补充细节后评审"
	case "reject":
		return "打回重写"
	}
	return "无法定级"
}

// statusCmd reports the connections and what the knowledge base holds.
func statusCmd(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dsn := fs.String("dsn", envOr("PRIOR_DSN", "postgres://postgres:postgres@127.0.0.1:5433/prior"), "PostgreSQL 连接串")
	ollama := fs.String("ollama", envOr("PRIOR_OLLAMA", "http://127.0.0.1:11434"), "Ollama 地址")
	embedModel := fs.String("embed", envOr("PRIOR_EMBED", "bge-m3:latest"), "embedding 模型")
	mf := addModelFlags(fs)
	if _, err := parseArgs(fs, args); err != nil {
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	em := embed.New(*ollama, *embedModel, mf.client(*ollama, 0, 0))
	fmt.Println("先例 · 环境自检")
	if models, err := em.Tags(ctx); err != nil {
		fmt.Printf("  Ollama     ✗ %v\n", err)
	} else if !llm.HasModel(models, *embedModel, llm.ProtocolOllama) {
		fmt.Printf("  Ollama     ✗ %s（没有 embedding 模型 %s，现有 %d 个）\n", *ollama, *embedModel, len(models))
	} else {
		fmt.Printf("  Ollama     ✓ %s（embedding %s，模型 %d 个）\n", *ollama, *embedModel, len(models))
	}
	// The language model answers where its own protocol says, which is not
	// necessarily Ollama any more.
	gen := em.LLM
	if listed, err := gen.Models(ctx); err != nil {
		fmt.Printf("  语言模型   ✗ %s %v\n", mf.where(*ollama), err)
	} else if !llm.HasModel(listed, gen.Model(), gen.Protocol()) {
		fmt.Printf("  语言模型   ✗ %s（端点列出 %d 个模型，没有它）\n", mf.where(*ollama), len(listed))
	} else {
		fmt.Printf("  语言模型   ✓ %s\n", mf.where(*ollama))
	}
	st, err := store.Open(ctx, *dsn)
	if err != nil {
		fmt.Printf("  PostgreSQL ✗ %v\n", err)
		return
	}
	defer st.Close()
	docs, chunks, findings, records, err := st.Stats(ctx)
	if err != nil {
		fmt.Printf("  PostgreSQL ✓ %s（统计失败：%v）\n", *dsn, err)
		return
	}
	fmt.Printf("  PostgreSQL ✓ %s（pgvector %v）\n", *dsn, st.HasVector())
	fmt.Printf("  知识库      文档 %d，块 %d，发现 %d，变更记录 %d\n", docs, chunks, findings, records)
	if list, err := st.ListDocuments(ctx, 3); err == nil && len(list) > 0 {
		fmt.Println("  最近入库")
		for _, d := range list {
			// A document that states no date keeps the file time in the
			// store, so the list says so rather than passing it off as the
			// document's own date.
			date := d.Date.Format("2006-01-02")
			if d.DateSource == "missing" {
				date = "日期未知"
			}
			fmt.Printf("     %s  %s  %s\n", date, truncate(d.Title, 40), d.Status)
		}
	}
}

// dateCoverage prints where the archived documents' dates came from: a
// document with no date of its own is only orderable by when it was filed.
func dateCoverage(st *store.Store, ctx context.Context, total int) {
	sources, err := st.DateSources(ctx)
	if err != nil || total == 0 {
		return
	}
	known := 0
	order := []string{"manifest", "field", "file_name", "file_name_short", "text", "change_log", "missing"}
	parts := make([]string, 0, len(order))
	for _, source := range order {
		if n := sources[source]; n > 0 {
			parts = append(parts, source+" "+strconv.Itoa(n))
			if source != "missing" {
				known += n
			}
		}
	}
	fmt.Printf("日期覆盖    %d/%d（%.1f%%）  %s\n", known, total, float64(known)*100/float64(total), strings.Join(parts, " / "))
}

// moduleCoverage prints how much of the archive has been assigned a system,
// with the systems it is grouped into.
func moduleCoverage(st *store.Store, ctx context.Context, total int) {
	counts, err := st.ModuleCounts(ctx)
	if err != nil || total == 0 {
		return
	}
	named := 0
	parts := make([]string, 0, 6)
	for i, entry := range counts {
		named += entry.Count
		if i < 6 {
			parts = append(parts, entry.Name+" "+strconv.Itoa(entry.Count))
		}
	}
	fmt.Printf("模块覆盖    %d/%d（%.1f%%）  %s\n", named, total,
		float64(named)*100/float64(total), strings.Join(parts, " / "))
}

// pointsCmd shows the function point ledger and settles the duplicates the
// identity resolver could only offer: a candidate match is a suggestion,
// and merging is a person's decision.
func pointsCmd(args []string) {
	fs := flag.NewFlagSet("points", flag.ExitOnError)
	dsn := fs.String("dsn", envOr("PRIOR_DSN", "postgres://postgres:postgres@127.0.0.1:5433/prior"), "PostgreSQL 连接串")
	merge := fs.String("merge", "", "把 FROM 并入 TO，如 -merge 12 34")
	candidatesOnly := fs.Bool("candidates", false, "只看待确认候选")
	rest, err := parseArgs(fs, args)
	if err != nil {
		os.Exit(2)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "points: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	if *merge != "" {
		fields := strings.Fields(*merge)
		if len(fields) < 2 {
			fmt.Fprintln(os.Stderr, "points: -merge 需要两个功能点编号")
			os.Exit(2)
		}
		from, err1 := strconv.ParseInt(fields[0], 10, 64)
		to, err2 := strconv.ParseInt(fields[1], 10, 64)
		if err1 != nil || err2 != nil {
			fmt.Fprintln(os.Stderr, "points: -merge 的编号不是数字")
			os.Exit(2)
		}
		moved, err := st.MergeFunctionPoints(ctx, from, to)
		if err != nil {
			fmt.Fprintf(os.Stderr, "points: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("已合并：功能点 %d 的 %d 条历史并入 %d\n", from, moved, to)
		return
	}
	_ = rest

	points, err := st.FunctionPoints(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "points: %v\n", err)
		os.Exit(1)
	}
	records, err := st.Ledger(ctx, 500)
	if err != nil {
		fmt.Fprintf(os.Stderr, "points: %v\n", err)
		os.Exit(1)
	}
	byPoint := map[int64][]store.ChangeRecord{}
	candidates := map[int64][]store.ChangeRecord{}
	for _, rec := range records {
		byPoint[rec.PointID] = append(byPoint[rec.PointID], rec)
		if rec.MatchKind == "candidate" && rec.MatchedPointID != 0 {
			candidates[rec.MatchedPointID] = append(candidates[rec.MatchedPointID], rec)
		}
	}
	fmt.Printf("功能点 %d 个，变更记录 %d 条\n", len(points), len(records))
	for _, point := range points {
		history := byPoint[point.ID]
		pending := candidates[point.ID]
		if *candidatesOnly && len(pending) == 0 {
			continue
		}
		kind := "new"
		if len(history) > 0 {
			kind = history[0].MatchKind
		}
		fmt.Printf("#%-4d %-42s 记录 %-3d 最近 %s（%s） %s\n", point.ID, truncate(point.Label, 42),
			len(history), truncate(point.LatestRule, 34), kind, latestStatus(history))
		// The trail is what the ledger is for: one line per record, newest
		// first, by the day of the document it came from.
		for i, rec := range history {
			if i == 8 {
				fmt.Printf("      ……还有 %d 条\n", len(history)-i)
				break
			}
			when := "日期未知"
			if !rec.DocDate.IsZero() && rec.DocDateSource != "" && rec.DocDateSource != "missing" {
				when = rec.DocDate.Format("2006-01-02")
			}
			from := ""
			if rec.Before != "" {
				from = "（原：" + truncate(rec.Before, 24) + "）"
			}
			fmt.Printf("      %s %-9s %-28s %s%s\n", when, ledgerKindName(rec.Kind),
				truncate("《"+rec.DocTitle+"》", 28), truncate(rec.After, 40), from)
		}
		for _, rec := range pending {
			fmt.Printf("      候选：#%-4d %-40s 相似度 %.2f 来自《%s》\n",
				rec.PointID, truncate(rec.Label, 40), rec.MatchScore, truncate(rec.DocTitle, 20))
		}
	}
}

// ledgerKindName is what a record's kind reads as on the command line.
func ledgerKindName(kind string) string {
	switch kind {
	case "baseline":
		return "基线"
	case "modified":
		return "修改"
	case "clarified":
		return "澄清"
	case "deprecated":
		return "废弃"
	}
	return kind
}

// latestStatus is the state of a point's newest record.
func latestStatus(history []store.ChangeRecord) string {
	if len(history) == 0 {
		return "无"
	}
	switch history[0].Status {
	case "confirmed":
		return "已确认"
	case "rejected":
		return "已驳回"
	default:
		return "待确认"
	}
}

// modulesCmd re-derives the system each archived document belongs to. The
// library groups by this name, so it has to be one name per system: the
// deterministic matcher runs first on the document's own words, and only a
// document that names none is put to the model, with the systems already in
// the library as its vocabulary. It previews by default; -apply writes.
func modulesCmd(args []string) {
	fs := flag.NewFlagSet("modules", flag.ExitOnError)
	dsn := fs.String("dsn", envOr("PRIOR_DSN", "postgres://postgres:postgres@127.0.0.1:5433/prior"), "PostgreSQL 连接串")
	ollama := fs.String("ollama", envOr("PRIOR_OLLAMA", "http://127.0.0.1:11434"), "Ollama 地址")
	mf := addModelFlags(fs)
	apply := fs.Bool("apply", false, "写回数据库")
	numCtx := fs.Int("num-ctx", llm.DefaultNumCtx, "模型上下文窗口")
	timeoutSec := fs.Int("timeout-sec", 600, "单次模型调用的超时（秒）")
	limit := fs.Int("limit", 0, "只处理前 N 份，0 为全部")
	rest, err := parseArgs(fs, args)
	if err != nil {
		os.Exit(2)
	}
	dir := firstOr(rest, "")

	ctx := context.Background()
	st, err := store.Open(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "modules: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()
	docs, err := st.ListDocuments(ctx, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "modules: %v\n", err)
		os.Exit(1)
	}
	known, err := st.Modules(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "modules: %v\n", err)
		os.Exit(1)
	}
	vocabulary := ingest.Systems()
	for _, name := range known {
		if !containsString(vocabulary, name) && !ingest.SystemKnown(name) {
			vocabulary = append(vocabulary, name)
		}
	}
	// The command asks the language model alone; Ollama holds the embeddings
	// only where another command embeds.
	em := embed.New(*ollama, *mf.model, mf.client(*ollama, *numCtx, *timeoutSec))

	var (
		fromTitle, fromBody, guessed, unnamed, unchanged int
		asked                                            int
		processed                                        int
	)
	for _, doc := range docs {
		if *limit > 0 && processed >= *limit {
			break
		}
		processed++
		headings, sample := documentText(dir, doc)
		// Whose words decided matters: a title names the system, a heading
		// usually does, body text often only mentions it.
		name, ok := ingest.SystemIn(doc.Title)
		source := "标题"
		if !ok && len(headings) > 0 {
			name, ok = ingest.SystemIn(headings...)
			source = "章节"
		}
		if !ok && sample != "" {
			name, ok = ingest.SystemIn(sample)
			source = "正文（较弱）"
		}
		if !ok {
			asked++
			guessedName, err := agent.InferModule(ctx, em, agent.DocumentBrief{
				Title: doc.Title, Date: doc.Date, Module: doc.Module, DocType: doc.DocType,
			}, headings, sample, vocabulary, *mf.model, 0)
			if err != nil {
				fmt.Printf("  问模型失败  %-40s %v\n", truncate(doc.Title, 40), truncate(err.Error(), 60))
				continue
			}
			name, source = guessedName, "模型"
			if !ingest.SystemKnown(name) {
				source = "模型（新系统）"
			}
		}
		if name == "" {
			unnamed++
			continue
		}
		if doc.Module == name {
			unchanged++
			continue
		}
		switch source {
		case "标题":
			fromTitle++
		case "正文（较弱）":
			fromBody++
		default:
			guessed++
		}
		was := "（无）"
		if doc.Module != "" {
			was = truncate(doc.Module, 20)
		}
		fmt.Printf("  %-22s → %-22s %s\n", was, truncate(name, 22), source)
		if *apply {
			if err := st.SetDocumentModule(ctx, doc.ID, name); err != nil {
				fmt.Printf("  写回失败    %-40s %v\n", truncate(doc.Title, 40), err)
			}
		}
	}
	fmt.Printf("处理 %d，按标题/章节判定 %d，按正文判定 %d（较弱），模型判定 %d，问模型 %d，仍未定 %d，无需改 %d\n",
		processed, fromTitle, fromBody, guessed, asked, unnamed, unchanged)
	if !*apply {
		fmt.Println("（预览，未写回；加 -apply 落库）")
	}
}

// datesCmd reads and corrects the day each archived document was written.
// The ledger reads a function point's trail by that date, so a document whose
// date is wrong — or unknown — puts its entries in the wrong place: this
// command shows what every document states, records a day a person gives, and
// re-reads the folder to pick up a rule that has changed.
func datesCmd(args []string) {
	fs := flag.NewFlagSet("dates", flag.ExitOnError)
	dsn := fs.String("dsn", envOr("PRIOR_DSN", "postgres://postgres:postgres@127.0.0.1:5433/prior"), "PostgreSQL 连接串")
	unknown := fs.Bool("unknown", false, "只列日期未知的文档")
	set := fs.Bool("set", false, "人工补录：-set <编号> <日期>")
	rescan := fs.Bool("rescan", false, "重新解析每份文件，只更新日期与版本序")
	rest, err := parseArgs(fs, args)
	if err != nil {
		os.Exit(2)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, *dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dates: %v\n", err)
		os.Exit(1)
	}
	defer st.Close()

	switch {
	case *set:
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "dates: -set 需要 <编号> <日期>，如 -set 42 2025-06-30")
			os.Exit(2)
		}
		id, err := strconv.ParseInt(rest[0], 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dates: 编号 %q 不是数字\n", rest[0])
			os.Exit(2)
		}
		date, err := time.ParseInLocation("2006-01-02", rest[1], time.Local)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dates: 日期 %q 应写成 2025-06-30\n", rest[1])
			os.Exit(2)
		}
		if err := st.SetDocumentDate(ctx, id, date); err != nil {
			fmt.Fprintf(os.Stderr, "dates: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("文档 %d 的成稿日期记为 %s（来源 manual）\n", id, rest[1])
	case *rescan:
		datesRescan(ctx, st, firstOr(rest, ""))
	default:
		docs, err := st.ListDocuments(ctx, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dates: %v\n", err)
			os.Exit(1)
		}
		dated := 0
		for _, doc := range docs {
			if doc.DateSource == "missing" || doc.Date.IsZero() {
				if !*unknown {
					continue
				}
				fmt.Printf("%-5d %-30s %s\n", doc.ID, "日期未知", truncate(doc.Title, 44))
				continue
			}
			if *unknown {
				continue
			}
			dated++
			fmt.Printf("%-5d %-30s %s\n", doc.ID,
				fmt.Sprintf("%s（%s·%.2f）", doc.Date.Format("2006-01-02"), doc.DateSource, doc.DateConf),
				truncate(doc.Title, 44))
		}
		sources, err := st.DateSources(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "dates: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("共 %d 份，有日期 %d 份\n", len(docs), dated)
		printDateSources(sources)
	}
}

// datesRescan re-reads every document of the library from its file and writes
// back the date and the version order the current rules find. It leaves the
// chunks, the vectors and the day a record was already recorded on alone: a
// re-read of a corrected rule is meant to be safe to run over the corpus
// again, and the entries of the ledger are moved only by an explicit -set.
func datesRescan(ctx context.Context, st *store.Store, dir string) {
	docs, err := st.ListDocuments(ctx, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dates: %v\n", err)
		os.Exit(1)
	}
	var rescanned, changed, unreadable int
	for _, doc := range docs {
		path := sourcePath(dir, doc)
		if path == "" {
			unreadable++
			fmt.Printf("  读不到文件  %-40s %s\n", truncate(doc.Title, 40), doc.SourceURI)
			continue
		}
		parsed, err := ingest.Parse(path)
		if err != nil {
			unreadable++
			fmt.Printf("  解析失败    %-40s %v\n", truncate(doc.Title, 40), truncate(err.Error(), 60))
			continue
		}
		meta := parsed.Meta
		rank := ingest.VersionRank(meta.Version)
		rescanned++
		was := "日期未知"
		if !doc.Date.IsZero() {
			was = fmt.Sprintf("%s（%s）", doc.Date.Format("2006-01-02"), doc.DateSource)
		}
		now := "日期未知"
		source := meta.DateSource
		if !meta.Date.IsZero() {
			now = fmt.Sprintf("%s（%s）", meta.Date.Format("2006-01-02"), source)
		} else {
			source = "missing"
		}
		if was == now && rank == doc.VersionRank {
			continue
		}
		changed++
		fmt.Printf("  %-34s → %-34s %s\n", was, now, truncate(doc.Title, 34))
		if err := st.UpdateDocumentDate(ctx, doc.ID, meta.Date, source, meta.Confidence); err != nil {
			fmt.Printf("  写回失败    %-40s %v\n", truncate(doc.Title, 40), err)
			continue
		}
		if err := st.SetDocumentVersionRank(ctx, doc.ID, rank); err != nil {
			fmt.Printf("  写回失败    %-40s %v\n", truncate(doc.Title, 40), err)
		}
	}
	fmt.Printf("重读 %d 份，日期或版本序有变化 %d 份，读不到 %d 份\n", rescanned, changed, unreadable)
	sources, err := st.DateSources(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dates: %v\n", err)
		os.Exit(1)
	}
	printDateSources(sources)
}

// sourcePath is where a document's file is now: beside the folder given, or
// where it was read from.
func sourcePath(dir string, doc store.Doc) string {
	candidates := []string{}
	if dir != "" {
		candidates = append(candidates, filepath.Join(dir, filepath.Base(doc.SourceURI)))
	}
	candidates = append(candidates, doc.SourceURI)
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// printDateSources is where the library's dates came from, which is how a
// reader judges how much of the trail can be ordered by the documents' own
// dates.
func printDateSources(sources map[string]int) {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Printf("  %-16s %d\n", name, sources[name])
	}
}

// documentText reads a document's headings and an opening sample, from the
// file on disk when it is there.
func documentText(dir string, doc store.Doc) ([]string, string) {
	candidates := []string{doc.SourceURI}
	if dir != "" {
		candidates = append([]string{filepath.Join(dir, filepath.Base(doc.SourceURI))}, candidates...)
	}
	for _, candidate := range candidates {
		parsed, err := ingest.Parse(candidate)
		if err != nil {
			continue
		}
		headings := make([]string, 0, 32)
		var sample strings.Builder
		for _, block := range parsed.Blocks {
			if block.Kind == ingest.KindHeading && strings.TrimSpace(block.Text) != "" {
				headings = append(headings, strings.TrimSpace(block.Text))
			}
			if sample.Len() < 2000 {
				sample.WriteString(block.Plain())
				sample.WriteString("\n")
			}
		}
		return headings, sample.String()
	}
	return nil, ""
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// parseArgs parses a subcommand's arguments with the flags in any position.
// flag.Parse stops at the first argument that is not a flag, so
// `analyze doc.md -out r.md` would silently drop -out: the flags are
// collected first, and the rest is the positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	flags := make([]string, 0, len(args))
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			rest = append(rest, args[i+1:]...)
			break
		}
		if len(arg) < 2 || arg[0] != '-' {
			rest = append(rest, arg)
			continue
		}
		flags = append(flags, arg)
		name := strings.TrimLeft(arg, "-")
		hasValue := false
		if i := strings.IndexByte(name, '='); i >= 0 {
			name, hasValue = name[:i], true
		}
		if hasValue || fs.Lookup(name) == nil || isBoolFlag(fs.Lookup(name)) {
			continue
		}
		// The value is the next argument, which may not be taken as a
		// positional argument.
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	return append(fs.Args(), rest...), nil
}

// firstOr is the first positional argument, or the fallback.
func firstOr(args []string, fallback string) string {
	if len(args) > 0 {
		return args[0]
	}
	return fallback
}

// isBoolFlag reports whether a flag takes no value.
func isBoolFlag(f *flag.Flag) bool {
	if f == nil {
		return false
	}
	type boolFlag interface{ IsBoolFlag() bool }
	if b, ok := f.Value.(boolFlag); ok {
		return b.IsBoolFlag()
	}
	return false
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// documents lists the documents of a folder, in name order.
func documents(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if ingest.Supported(path) {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out, nil
}

func counts(m map[string]int) string {
	type kv struct {
		k string
		n int
	}
	list := make([]kv, 0, len(m))
	for k, n := range m {
		list = append(list, kv{k, n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		return list[i].k < list[j].k
	})
	parts := make([]string, 0, len(list))
	for _, it := range list {
		parts = append(parts, fmt.Sprintf("%s %d", it.k, it.n))
	}
	return strings.Join(parts, "  ")
}

func average(total, n int) float64 {
	if n == 0 {
		return 0
	}
	return float64(total) / float64(n)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
