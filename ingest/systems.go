package ingest

import "strings"

// A document's 所属系统 is what the library groups by and what the function
// point ledger keys its modules on, so it has to be one name per system
// rather than whatever the file was called. The list below is a sample
// vocabulary for common product areas: replace or extend it with the systems
// your own corpus writes about. The first entry that matches any of a
// document's texts wins, most specific first. A document that matches none is
// left unnamed rather than guessed: the analysis may then ask the model, with
// this list as the vocabulary it must choose from.
var knownSystems = []struct {
	name    string
	aliases []string
}{
	{"数字资产管理系统", []string{"数字资产管理系统", "数字资产系统", "数资系统", "DAM管理系统", "DAM"}},
	{"媒资管理系统", []string{"媒资管理系统", "媒资后台", "媒资系统", "媒资"}},
	{"SaaS 官网", []string{"SaaS 官网", "SaaS官网"}},
	{"企业内容平台", []string{"企业内容平台", "内容平台", "工作台"}},
	{"智能视频生成", []string{"智能视频生成", "视频生成平台", "视频合成", "视频脚本", "视频制作平台"}},
	{"智能海报生成", []string{"智能海报", "海报生成"}},
	{"智能编目与 AI 应用", []string{"智能编目", "AI编目", "批量编目", "AI应用", "智能策划"}},
	{"用户管理系统", []string{"用户管理系统", "用户管理后台", "用户注册", "登录日志", "用户主页", "个人主页"}},
	{"在线课程与考试", []string{"在线课程", "课程管理", "课程列表", "考试评测", "试题评测"}},
	{"创作中心", []string{"创作中心", "上传功能"}},
	{"赛事活动", []string{"机器人大赛", "演讲大赛", "赛事", "大赛"}},
	{"企业服务平台", []string{"企业数字化", "赋能平台", "企业服务平台"}},
	{"园区数字资产平台", []string{"园区数字资产", "视听数字资产", "数字资产平台"}},
	{"企业资质服务平台", []string{"专精特新", "企业资质"}},
	{"视听内容平台", []string{"视听内容", "视听平台"}},
	{"综合搜索", []string{"综合搜索"}},
	{"企业名片与机构专区", []string{"企业名片", "机构名片", "机构专区", "机构资料", "机构类型", "机构主页", "机构注册"}},
	{"知识库", []string{"知识库"}},
	{"专区与资产筛选", []string{"资产库筛选", "专区分类", "专区管理", "自选专区", "专区详情"}},
	{"官网运营活动", []string{"官网首页", "网站公告", "春节活动", "红包活动", "奖学金活动"}},
	{"会员与积分", []string{"会员中心", "会员积分", "积分"}},
	{"订单与履约", []string{"订单履约", "订单系统", "订单中心", "订单"}},
	{"支付与渠道", []string{"支付渠道", "支付平台", "支付系统", "支付"}},
	{"营销与优惠券", []string{"优惠券", "营销活动", "营销"}},
	{"风控与名单", []string{"风控名单", "名单同步", "风控"}},
}

// Systems is the vocabulary a document's system is chosen from: the
// canonical names, in match order.
func Systems() []string {
	out := make([]string, 0, len(knownSystems))
	for _, s := range knownSystems {
		out = append(out, s.name)
	}
	return out
}

// SystemIn names the system a document belongs to, from its title, its
// headings and its opening text, in that order of trust. It returns false
// when nothing in the list is present, which is the signal to ask the
// model rather than to guess.
func SystemIn(texts ...string) (string, bool) {
	for _, text := range texts {
		if strings.TrimSpace(text) == "" {
			continue
		}
		lowered := strings.ToLower(text)
		for _, system := range knownSystems {
			for _, alias := range system.aliases {
				if strings.Contains(lowered, strings.ToLower(alias)) {
					return system.name, true
				}
			}
		}
	}
	return "", false
}

// SystemKnown reports whether a name is one of the vocabulary's, which is
// how an answer the model gave freely is told from one from the list.
func SystemKnown(name string) bool {
	for _, system := range knownSystems {
		if system.name == name {
			return true
		}
	}
	return false
}
