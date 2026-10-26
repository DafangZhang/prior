package agent

import (
	"testing"

	"prior/store"
)

func fixtureRegistry() []store.FunctionPoint {
	return []store.FunctionPoint{
		{ID: 2, Module: "购物车系统", Function: "购买流程优化", Topic: "购买方式扩展",
			Label:      "购物车系统 · 购买流程优化 · 购买方式扩展",
			LatestRule: "支持机构用户前端购买单独产品、产品组合或自由组合"},
		{ID: 4, Module: "购物车系统", Function: "产品清单-概述", Topic: "产品配置支持",
			Label: "购物车系统 · 产品清单-概述 · 产品配置支持"},
		{ID: 9, Module: "媒资管理系统", Function: "专区管理", Topic: "分区多级分区",
			Label: "媒资管理系统 · 专区管理 · 分区多级分区"},
	}
}

// TestResolvePointExactAndNormalized covers the two automatic matches: the
// same three parts, and the same three parts written with punctuation,
// spacing or a revision marker in between.
func TestResolvePointExactAndNormalized(t *testing.T) {
	registry := fixtureRegistry()
	exact := resolvePoint("购物车系统", "购买流程优化", "购买方式扩展",
		"购物车系统 · 购买流程优化 · 购买方式扩展", "规则", registry, IdentityThreshold)
	if exact.Kind != "exact" || exact.Point.ID != 2 {
		t.Errorf("exact match failed: %+v", exact)
	}
	// The second run of the same document named the same rule differently
	// but comparably: spacing, a middle dot, a full-width slash.
	normalized := resolvePoint("购物车系统", "购买流程优化", "购买方式扩展",
		"购物车系统·购买流程优化／购买方式扩展", "规则", registry, IdentityThreshold)
	if normalized.Kind != "exact" && normalized.Kind != "normalized" {
		t.Errorf("normalized match failed: %+v", normalized)
	}
	if normalized.Point.ID != 2 {
		t.Errorf("normalized match hit the wrong point: %+v", normalized)
	}
}

// TestResolvePointCandidate covers the measured drift: the same rule named
// with different words must not silently become a new point, but must not
// be merged by the machine either.
func TestResolvePointCandidate(t *testing.T) {
	registry := fixtureRegistry()
	drifted := resolvePoint("购物车系统", "机构用户购买方式", "购买选项",
		"购物车系统 · 机构用户购买方式 · 购买选项",
		"同步支持机构用户在前端购买单独产品、产品组合或自由组合", registry, IdentityThreshold)
	if drifted.Kind != "normalized" {
		t.Fatalf("a renamed function whose rule is the same rule should attach, got %+v", drifted)
	}
	if drifted.Point.ID != 2 {
		t.Errorf("it should attach to the purchase-flow point, got %d", drifted.Point.ID)
	}
	if drifted.Score < IdentityThreshold || drifted.Score > 1 {
		t.Errorf("score out of range: %v", drifted.Score)
	}
	unrelated := resolvePoint("媒资管理系统", "素材卡片", "样式",
		"媒资管理系统 · 素材卡片 · 样式", "素材卡片支持方形与圆角两种样式", registry, IdentityThreshold)
	if unrelated.Kind != "new" {
		t.Errorf("an unrelated point should be new, got %+v", unrelated)
	}
}

// TestPointSimilarityIsBounded covers the metric the threshold sits on.
func TestPointSimilarityIsBounded(t *testing.T) {
	if got := pointSimilarity("购物车系统购买流程优化", "购物车系统购买流程优化"); got != 1 {
		t.Errorf("identical strings score %v", got)
	}
	if got := pointSimilarity("购物车系统", ""); got != 0 {
		t.Errorf("an empty side scores %v", got)
	}
	high := pointSimilarity("购物车系统购买流程优化购买方式扩展", "购物车系统购买流程优化购买方式扩展二期")
	if high < 0.6 {
		t.Errorf("a revision marker should not change the score much: %v", high)
	}
	low := pointSimilarity("媒资管理系统专区管理", "官网首页banner")
	if low > 0.3 {
		t.Errorf("unrelated names score too high: %v", low)
	}
}

// TestNormalizePointDropsRevisionMarkers covers what must not identify a
// point: punctuation, width and revision wording.
func TestNormalizePointDropsRevisionMarkers(t *testing.T) {
	cases := map[string]string{
		"购物车系统 · 购买流程优化 · 购买方式扩展": "购物车系统购买流程优化购买方式扩展",
		"购物车系统/购买流程优化":            "购物车系统购买流程优化",
		"机构专区与名片系统2.0 二期":         "机构专区与名片系统",
		"SaaS 官网":                 "saas官网",
	}
	for in, want := range cases {
		if got := normalizePoint(in); got != want {
			t.Errorf("normalizePoint(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestResolvePointAttachesOnRenamedRule covers the case a document revision
// actually produces: the same rule, renamed by the model, with its text
// either identical or only reworded. The ledger must write onto the existing
// point there, or a version pair produces two histories instead of one.
func TestResolvePointAttachesOnRenamedRule(t *testing.T) {
	registry := []store.FunctionPoint{{
		ID: 11, Module: "SaaS春节活动系统", Function: "PC页面Banner图片替换", Topic: "图片替换规则",
		Label:      "SaaS春节活动系统 · PC页面Banner图片替换 · 图片替换规则",
		LatestRule: "页面头部Banner需替换为指定压缩文件中的图片",
	}}
	// Same rule text, function renamed by the model: attach.
	same := resolvePoint("SaaS春节活动系统", "PC页面头部Banner替换", "页面元素替换",
		"SaaS春节活动系统 · PC页面头部Banner替换 · 页面元素替换",
		"页面头部Banner需替换为指定压缩文件中的图片", registry, IdentityThreshold)
	if same.Kind != "normalized" || same.Point.ID != 11 {
		t.Errorf("an identical rule under a renamed function should attach, got %+v", same)
	}
	// A rule reworded past recognition under a partly related name: the
	// ledger says "look at this one", it does not decide.
	registry = append(registry, store.FunctionPoint{
		ID: 13, Module: "SaaS春节活动系统", Function: "专区名称修改", Topic: "名称变更规则",
		Label:      "SaaS春节活动系统 · 专区名称修改 · 名称变更规则",
		LatestRule: "专区名称需按指定规则修改",
	})
	reworded := resolvePoint("SaaS春节活动系统", "专区名称修改", "内容结构调整",
		"SaaS春节活动系统 · 专区名称修改 · 内容结构调整",
		"电商/医疗行业专区名称需修改为指定名称", registry, IdentityThreshold)
	if reworded.Kind != "candidate" || reworded.Point.ID != 13 {
		t.Errorf("a reworded rule should stay a suggestion, got %+v", reworded)
	}
	// The same rule with one detail added, under a related name: the same
	// point, and what changed is what the ledger records.
	registry = append(registry, store.FunctionPoint{
		ID: 14, Module: "SaaS春节活动系统", Function: "专区封面图修改", Topic: "封面图替换规则",
		Label:      "SaaS春节活动系统 · 专区封面图修改 · 封面图替换规则",
		LatestRule: "专区封面图需替换为指定压缩文件中的图片",
	})
	modified := resolvePoint("SaaS春节活动系统", "专区封面图替换", "视觉元素更新",
		"SaaS春节活动系统 · 专区封面图替换 · 视觉元素更新",
		"节日专区封面需替换为指定压缩文件中的图片", registry, IdentityThreshold)
	if modified.Kind != "normalized" || modified.Point.ID != 14 {
		t.Errorf("nearly the same rule should attach so the change is recorded, got %+v", modified)
	}
	// Unrelated function, similar generic rule: a suggestion at most.
	registry = append(registry, store.FunctionPoint{
		ID: 30, Module: "媒资管理系统", Function: "编目", Topic: "状态",
		Label:      "媒资管理系统 · 编目 · 状态",
		LatestRule: "编目状态变更需记录操作日志",
	})
	other := resolvePoint("媒资管理系统", "素材卡片", "样式",
		"媒资管理系统 · 素材卡片 · 样式", "卡片样式变更需记录操作日志", registry, IdentityThreshold)
	if other.Kind == "normalized" {
		t.Errorf("unrelated names must not auto-attach: %+v", other)
	}
}

// TestResolvePointIgnoresModuleDrift covers what the version pair measured:
// the model named the same system differently in two versions of one
// document, which must not hide the match. The function and topic carry the
// kinship instead.
func TestResolvePointIgnoresModuleDrift(t *testing.T) {
	registry := []store.FunctionPoint{{
		ID: 25, Module: "SaaS春节活动系统", Function: "专区名称修改", Topic: "名称变更",
		Label:      "SaaS春节活动系统 · 专区名称修改 · 名称变更",
		LatestRule: "专区名称需按指定规则修改",
	}}
	drift := resolvePoint("SaaS数字资产云平台", "专区名称修改", "名称变更变更",
		"SaaS数字资产云平台 · 专区名称修改 · 名称变更变更",
		"专区名称修改为智慧直播数字资产专区和智慧医疗数字资产专区", registry, IdentityThreshold)
	if drift.Kind != "normalized" || drift.Point.ID != 25 {
		t.Errorf("a module renamed between versions must not hide the match, got %+v", drift)
	}
}

// TestResolvePointVersionPairBars covers the measured version pair: one
// point renamed to something four fifths alike attaches, and one renamed
// past that only asks. The numbers are the real ones from the pair
// SaaS官网春节活动需求文档 V1.0.0 → V1.1.0。
func TestResolvePointVersionPairBars(t *testing.T) {
	registry := []store.FunctionPoint{
		{ID: 36, Module: "SaaS春节活动系统", Function: "PC页面头部Banner", Topic: "图片替换",
			Label:      "SaaS春节活动系统 · PC页面头部Banner · 图片替换",
			LatestRule: "页面头部Banner需替换为压缩文件中的【活动主视觉.jpg】"},
		{ID: 40, Module: "SaaS春节活动系统", Function: "落地页面新增", Topic: "板块新增",
			Label:      "SaaS春节活动系统 · 落地页面新增 · 板块新增",
			LatestRule: "节日素材专区下新增2个专区板块"},
	}
	attached := resolvePoint("SaaS春节活动系统", "页面头部Banner修改", "图片替换",
		"SaaS春节活动系统 · 页面头部Banner修改 · 图片替换",
		"页面头部Banner需替换为压缩文件中的【活动主视觉.jpg】", registry, IdentityThreshold)
	if attached.Kind != "normalized" || attached.Point.ID != 36 {
		t.Errorf("a four-fifths-alike rename should attach, got %+v", attached)
	}
	suggested := resolvePoint("SaaS春节活动系统", "专区封面图修改", "封面图替换",
		"SaaS春节活动系统 · 专区封面图修改 · 封面图替换",
		"节日专区封面修改为压缩文件中的【节日专区封面.png】", registry, IdentityThreshold)
	if suggested.Kind == "normalized" {
		t.Errorf("a rename past the bar should stay a suggestion: %+v", suggested)
	}
}

// TestLocateQuoteTolerance covers what counts as the document's own
// sentence: a quote with an embellished tail still counts, a paraphrase
// does not, and a sentence from another document never does.
func TestLocateQuoteTolerance(t *testing.T) {
	chunks := []store.Chunk{{Index: 0, Path: []string{"3 功能"}, Content: "3.1 列表\n支持按权归属筛选资产，默认展示全部权归属。" +
		"\n3.2 导出\n导出时按当前筛选结果生成文件。"}}
	cases := []struct {
		name  string
		quote string
		want  bool
	}{
		{"verbatim", "支持按权归属筛选资产，默认展示全部权归属。", true},
		{"trailing rewording", "支持按权归属筛选资产，默认展示全部权归属，并支持多选。", true},
		{"prefix reworded", "需求：支持按权归属筛选资产，默认展示全部权归属。", true},
		{"punctuation differs", "支持按权归属筛选资产 默认展示全部权归属", true},
		{"paraphrase", "用户可以按照资产的权归属来做筛选，列表默认把所有的权归属都列出来。", false},
		{"other document", "本期新增机构用户购买单独产品与组合产品的能力。", false},
		{"too short", "支持", false},
	}
	for _, c := range cases {
		_, ok := locateQuote(chunks, c.quote)
		if ok != c.want {
			t.Errorf("%s: locateQuote(%q) = %v, want %v", c.name, c.quote, ok, c.want)
		}
	}
	if !inDocument(chunks, "导出时按当前筛选结果生成文件。") {
		t.Error("a quote from a later chunk was not found")
	}
}
