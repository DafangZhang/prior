package main

import "time"

// Mock fixtures for layout verification. Backend integration is a future task.

type doc struct {
	ID          int64
	Title       string
	Format      string
	ProductLine string
	Module      string
	Status      string // active / superseded / draft
	Chars       int
	Chunks      int
	Imported    string
	// Date is the document's own date, read from its name, its fields or
	// its change log; DateSource says where it came from, and a document
	// that names no date has none.
	Date           time.Time
	DateSource     string
	DateConfidence float64
	DocType        string
	SourceURI      string
}

// dateSourceLabel is what the library shows of a date's origin.
func dateSourceLabel(source string) string {
	switch source {
	case "manifest":
		return T("from the manifest")
	case "field":
		return T("stated in the document")
	case "file_name":
		return T("from the file name")
	case "change_log":
		return T("from its change log")
	case "text":
		return T("mentioned in its text")
	case "file_name_short":
		return T("from the file name, year uncertain")
	}
	return T("unknown")
}

type run struct {
	ID       int64
	Title    string
	Status   string // running / succeeded / degraded / failed / canceled
	Stage    string
	Findings [4]int // S0, S1, S2, S3
	// Counts are the findings by type, Total and Pending the list's
	// headline figures; Verdict is the pre-review's conclusion.
	Counts    map[string]int
	Total     int
	Pending   int
	Verdict   string
	StartedAt string
	Elapsed   string
	DocsHit   int
	// DocID is the document the run reviewed, which the report shows with
	// its problems in place.
	DocID int64
}

type finding struct {
	ID          string
	Type        string // change_signal / logic_gap / ambiguity / one_liner
	Subtype     string
	Severity    string // S0 / S1 / S2 / S3
	Confidence  float64
	Title       string
	Detail      string
	NewQuote    string
	NewPath     string
	OldQuote    string
	OldPath     string
	OldDocTitle string
	Question    string
	Impact      string
	Status      string // open / confirmed / dismissed / pending / parked
	// Fix and Note are what a reviewer leaves behind.
	Fix  string
	Note string
}

type note struct {
	ID       int64
	Title    string
	Time     string
	Read     bool
	ReportID string
}

func fixtureDocs() []doc {
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.Local) }
	return []doc{
		{ID: 1, Title: "会员积分规则 PRD v2.1", Format: "PDF", ProductLine: "会员", Module: "会员",
			Status: "active", Chars: 18420, Chunks: 42, Imported: "2025-10-12 09:14",
			Date: at(2025, time.October, 12), DateSource: "field", DateConfidence: 0.95, DocType: "requirement"},
		{ID: 2, Title: "订单履约流程 v1.4", Format: "MD", ProductLine: "交易", Module: "交易",
			Status: "active", Chars: 12680, Chunks: 28, Imported: "2025-10-10 15:02",
			Date: at(2025, time.October, 10), DateSource: "file_name", DateConfidence: 0.9, DocType: "requirement"},
		{ID: 3, Title: "支付渠道路由方案 v3.0", Format: "PDF", ProductLine: "支付", Module: "支付",
			Status: "superseded", Chars: 22150, Chunks: 55, Imported: "2025-09-28 11:47",
			Date: at(2025, time.September, 28), DateSource: "field", DateConfidence: 0.95, DocType: "requirement"},
		{ID: 4, Title: "优惠券叠加规则 v0.9", Format: "DOCX", ProductLine: "营销", Module: "营销",
			Status: "draft", Chars: 9840, Chunks: 18, Imported: "2025-10-14 10:31",
			Date: at(2025, time.October, 14), DateSource: "change_log", DateConfidence: 0.75, DocType: "requirement"},
		{ID: 5, Title: "消息推送中心 v2.2", Format: "PDF", ProductLine: "基础服务", Module: "基础服务",
			Status: "active", Chars: 15730, Chunks: 37, Imported: "2025-10-08 14:26",
			Date: at(2025, time.October, 8), DateSource: "field", DateConfidence: 0.95, DocType: "requirement"},
		{ID: 6, Title: "风控名单同步方案 v1.1", Format: "MD", ProductLine: "风控", Module: "风控",
			Status: "superseded", Chars: 8460, Chunks: 21, Imported: "2025-09-25 16:58",
			DocType: "requirement"},
	}
}

func fixtureRuns() []run {
	findings := fixtureFindings()
	severity, types := summarizeFindings(findings)
	return []run{
		{ID: 1, Title: "订单履约 × 支付渠道路由 对比分析", Status: "succeeded", Stage: "done",
			Findings: severity, Counts: types, Total: len(findings), Pending: len(findings), Verdict: "clarify",
			StartedAt: "2025-10-14 10:02", Elapsed: "2m18s", DocsHit: 2, DocID: 2},
		{ID: 2, Title: "优惠券叠加规则 首次分析", Status: "failed", Stage: "retrieval",
			Counts: map[string]int{}, StartedAt: "2025-10-13 19:44", Elapsed: "0m47s", DocsHit: 1, DocID: 4},
		{ID: 3, Title: "消息推送中心 × 会员积分 对比分析", Status: "succeeded", Stage: "done",
			Counts: map[string]int{}, StartedAt: "2025-10-11 09:17", Elapsed: "3m02s", DocsHit: 2, DocID: 1},
	}
}

// summarizeFindings is the run list's headline: findings by severity and by
// type. The fixture runs share the same findings in the sample; the real
// list fills the numbers from the database.
func summarizeFindings(findings []finding) ([4]int, map[string]int) {
	var severity [4]int
	types := map[string]int{}
	for _, f := range findings {
		if i := severityRankOf(f.Severity); i >= 0 && i < len(severity) {
			severity[i]++
		}
		types[f.Type]++
	}
	return severity, types
}

func severityRankOf(severity string) int {
	switch severity {
	case "S0":
		return 0
	case "S1":
		return 1
	case "S2":
		return 2
	case "S3":
		return 3
	}
	return -1
}

func fixtureFindings() []finding {
	return []finding{
		// change_signal: 3 条，含 S0
		{"f001", "change_signal", "deleted_clause", "S0", 0.94, "积分过期规则被删除，历史文档仍引用该条款",
			"新文档 v2.1 删除了「积分过期」章节（原 3.2 节），但历史文档 v1.7 的 3.2 节仍包含完整的过期规则。下游系统若依赖旧逻辑，将导致积分计算偏差。",
			"积分有效期由永久改为 24 个月，过期后自动清零。",
			"v2.1 → 第 4 章 积分生命周期",
			"积分永久有效，不设过期机制。",
			"v1.7 → 3.2 积分过期规则",
			"会员积分规则 PRD v1.7",
			"下游积分计算模块是否已同步删除过期逻辑？",
			"影响积分余额计算，可能导致多扣或少扣。",
			"open", "", ""},
		{"f002", "change_signal", "added_constraint", "S1", 0.87, "新增支付渠道路由优先级约束",
			"v3.0 新增「大额交易优先走直连渠道」约束，与 v1.4 的履约流程存在交叉影响。",
			"单笔金额超过 5000 元时，优先路由至直连支付渠道。",
			"v3.0 → 第 5 章 渠道路由策略",
			"所有交易按渠道可用性轮询路由，无金额限制。",
			"v1.4 → 4.1 支付渠道路由",
			"订单履约流程 v1.4",
			"履约流程是否已适配大额直连路由？",
			"可能导致大额交易路由失败或延迟。",
			"open", "", ""},
		{"f003", "change_signal", "modified_threshold", "S2", 0.78, "风控名单同步阈值从 100 条/分钟调整为 500 条/分钟",
			"v1.1 将同步阈值从 100 提升至 500，但未说明对下游消费方的带宽影响。",
			"名单同步阈值由 100 条/分钟调整为 500 条/分钟。",
			"v1.1 → 第 3 章 同步策略",
			"名单同步阈值为 100 条/分钟。",
			"v0.8 → 第 3 章 同步策略",
			"风控名单同步方案 v0.8",
			"下游消费方是否已适配更高的同步频率？",
			"可能导致下游消费积压或超时。",
			"open", "", ""},

		// logic_gap: 4 条
		{"f004", "logic_gap", "missing_branch", "S1", 0.82, "优惠券叠加规则缺少「已退款订单」的处理分支",
			"v0.9 描述了正常订单的叠加逻辑，但未提及已退款订单是否允许叠加使用。",
			"若订单已退款，优惠券不可叠加使用。",
			"v0.9 → 第 2 章 叠加规则",
			"未提及退款订单的处理方式。",
			"v0.9 → 第 2 章 叠加规则",
			"优惠券叠加规则 v0.9",
			"已退款订单是否允许叠加优惠券？",
			"可能导致资损或用户投诉。",
			"open", "", ""},
		{"f005", "logic_gap", "undefined_state", "S2", 0.75, "消息推送中心缺少推送失败后的重试上限定义",
			"v2.2 提到「推送失败时自动重试」，但未定义最大重试次数。",
			"推送失败时最多重试 3 次，间隔指数退避。",
			"v2.2 → 第 6 章 推送策略",
			"未定义重试上限。",
			"v2.2 → 第 6 章 推送策略",
			"消息推送中心 v2.2",
			"推送失败后的最大重试次数是多少？",
			"可能导致推送风暴或消息丢失。",
			"open", "", ""},
		{"f006", "logic_gap", "missing_rollback", "S2", 0.71, "支付渠道路由方案缺少路由失败后的回滚机制",
			"v3.0 描述了渠道路由策略，但未提及路由失败后的回滚流程。",
			"路由失败后自动回滚至默认渠道，并触发告警。",
			"v3.0 → 第 5 章 渠道路由策略",
			"未提及路由失败后的回滚流程。",
			"v3.0 → 第 5 章 渠道路由策略",
			"支付渠道路由方案 v3.0",
			"路由失败后如何回滚？",
			"可能导致支付失败或资金滞留。",
			"open", "", ""},
		{"f007", "logic_gap", "ambiguous_precedence", "S3", 0.68, "会员积分规则中「签到」与「消费」的优先级未定义",
			"v2.1 同时提到签到积分和消费积分，但未定义两者的计算优先级。",
			"签到积分与消费积分独立计算，互不干扰。",
			"v2.1 → 第 4 章 积分生命周期",
			"未定义签到与消费积分的计算优先级。",
			"v2.1 → 第 4 章 积分生命周期",
			"会员积分规则 PRD v2.1",
			"签到积分与消费积分是否独立计算？",
			"可能导致积分计算歧义。",
			"open", "", ""},

		// ambiguity: 3 条
		{"f008", "ambiguity", "vague_term", "S2", 0.73, "订单履约流程中「尽快处理」的定义模糊",
			"v1.4 提到「尽快处理退款申请」，但未定义具体时间上限。",
			"退款申请需在 24 小时内处理完成。",
			"v1.4 → 第 3 章 退款流程",
			"「尽快处理」未定义具体时间上限。",
			"v1.4 → 第 3 章 退款流程",
			"订单履约流程 v1.4",
			"「尽快处理」的具体时间上限是多少？",
			"可能导致退款延迟或用户投诉。",
			"open", "", ""},
		{"f009", "ambiguity", "unclear_scope", "S2", 0.70, "消息推送中心「活跃用户」的定义不明确",
			"v2.2 提到「向活跃用户推送」，但未定义活跃用户的判定标准。",
			"过去 7 天内有登录行为的用户视为活跃用户。",
			"v2.2 → 第 4 章 用户分群",
			"「活跃用户」未定义判定标准。",
			"v2.2 → 第 4 章 用户分群",
			"消息推送中心 v2.2",
			"「活跃用户」的判定标准是什么？",
			"可能导致推送范围偏差。",
			"open", "", ""},
		{"f010", "ambiguity", "vague_condition", "S3", 0.65, "风控名单同步方案中「高风险名单」的定义模糊",
			"v1.1 提到「优先同步高风险名单」，但未定义高风险名单的判定条件。",
			"命中风控规则且评分大于 80 的名单视为高风险。",
			"v1.1 → 第 2 章 名单分类",
			"「高风险名单」未定义判定条件。",
			"v1.1 → 第 2 章 名单分类",
			"风控名单同步方案 v1.1",
			"「高风险名单」的判定条件是什么？",
			"可能导致同步优先级混乱。",
			"open", "", ""},

		// one_liner: 2 条
		{"f011", "one_liner", "missing_field", "S3", 0.60, "会员积分规则缺少「积分兑换比例」字段",
			"v2.1 提到积分可兑换商品，但未定义兑换比例。",
			"100 积分可兑换 1 元商品。",
			"v2.1 → 第 5 章 积分兑换",
			"未定义积分兑换比例。",
			"v2.1 → 第 5 章 积分兑换",
			"会员积分规则 PRD v2.1",
			"积分兑换比例是多少？",
			"可能导致兑换歧义或资损。",
			"open", "", ""},
		{"f012", "one_liner", "missing_example", "S3", 0.55, "支付渠道路由方案缺少「大额交易」的示例",
			"v3.0 提到「大额交易优先走直连渠道」，但未定义大额交易的具体金额阈值。",
			"单笔金额超过 5000 元视为大额交易。",
			"v3.0 → 第 5 章 渠道路由策略",
			"未定义大额交易的金额阈值。",
			"v3.0 → 第 5 章 渠道路由策略",
			"支付渠道路由方案 v3.0",
			"大额交易的金额阈值是多少？",
			"可能导致路由歧义。",
			"open", "", ""},
	}
}

func fixtureNotes() []note {
	return []note{
		{1, "分析完成：订单履约 × 支付渠道路由", "2025-10-14 12:20", false, "1"},
		{2, "分析失败：优惠券叠加规则", "2025-10-13 19:45", false, "2"},
	}
}

// chunkPreview is the mock chunk breakdown the inspector shows: data, not
// interface copy, so it stays as the documents have it.
type chunkPreview struct {
	Label string
	Done  int
	Total int
}

var chunkPreviews = []chunkPreview{
	{"4.1 积分获取", 3, 3},
	{"4.2 积分消耗", 2, 3},
	{"4.3 积分兑换", 1, 2},
	{"4.4 积分过期", 0, 2},
}

// sourceSample is a line of a document's own text, as the inspector shows
// the source beside a finding.
const sourceSample = "积分有效期由永久改为 24 个月..."

// chunkHeading is the section the chunks above belong to.
const chunkHeading = "v2.1 → 第 4 章 积分生命周期"
