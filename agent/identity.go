package agent

import (
	"regexp"
	"strings"

	"prior/store"
)

// IdentityThreshold is how similar two function points must be before one is
// offered as a candidate for the other, and IdentityAttachThreshold is how
// similar they must be for the ledger to write onto the same point without
// asking: the same rule renamed by the model, which is what a document
// revision looks like. Below the attach bar the machine only suggests, since
// merging two different rules' histories is worse than keeping a row too
// many. Both bars also require the names to have kinship, so identical rule
// wording under unrelated functions stays a suggestion.
const (
	// IdentityThreshold is the bar for offering a candidate.
	IdentityThreshold = 0.65
	// candidateNameFloor is how much two names must have in common before
	// their rules are compared at all.
	candidateNameFloor = 0.3
	// attachNameFloor and attachRuleFloor are the evidence the ledger merges
	// on its own: names with kinship AND a rule that says the same thing.
	// Names this close are also the same function however the rule moved.
	attachNameFloor = 0.5
	attachRuleFloor = 0.8
	// attachLabelOnly is the name kinship that is enough on its own: four
	// fifths of the name's bigrams in common is the same function, however
	// the rule moved. Measured: a version pair renamed one point to
	// "页面头部Banner修改 · 图片替换" against "页面头部Banner · 图片替换"
	// (0.84) and another to names sharing 0.70–0.74, which stay suggestions.
	attachLabelOnly = 0.8
)

// pointMatch says which ledger point a new one is.
type pointMatch struct {
	Point store.FunctionPoint
	// Kind is exact (the three parts are equal), normalized (they are equal
	// after normalizing), candidate (a similar point exists, a person
	// decides), or new.
	Kind  string
	Score float64
}

// resolvePoint finds the ledger point a document's point belongs to: the
// same three parts, the same three parts written differently, or a
// candidate a person should look at.
func resolvePoint(module, function, topic, label, rule string, registry []store.FunctionPoint, threshold float64) pointMatch {
	want := pointKey(module, function, topic)
	wantLabel := normalizePoint(label)
	if wantLabel == "" {
		wantLabel = normalizePoint(want)
	}
	wantRule := normalizePoint(rule)
	wantFunction := normalizePoint(function + topic)
	best := pointMatch{Kind: "new"}
	for _, point := range registry {
		got := pointKey(point.Module, point.Function, point.Topic)
		if module == point.Module && function == point.Function && topic == point.Topic {
			return pointMatch{Point: point, Kind: "exact"}
		}
		if got == want {
			return pointMatch{Point: point, Kind: "normalized"}
		}
		// A rule restated with a new name is still the same rule: the rule
		// text counts as much as the name, but the names have to have some
		// kinship for the two to be the same function at all. The function
		// and its topic carry the kinship, not the module: the model names
		// the same system differently between two versions of a document
		// (measured: "SaaS春节活动系统" one version, "SaaS数字资产云平台"
		// the next), so a module in the name would hide a real match.
		nameScore := pointSimilarity(wantLabel, labelOf(point))
		if functionScore := pointSimilarity(wantFunction, functionOf(point)); functionScore > nameScore {
			nameScore = functionScore
		}
		ruleScore := pointSimilarity(wantRule, pointRule(point))
		if nameScore < candidateNameFloor {
			continue
		}
		score := nameScore
		if ruleScore > score {
			score = ruleScore
		}
		kind := "candidate"
		switch {
		case nameScore >= attachNameFloor && ruleScore >= attachRuleFloor:
			// Related names and a rule that says the same thing: this is a
			// revision of one rule, not two rules.
			kind = "normalized"
		case nameScore >= attachLabelOnly:
			// The name is the same; what the rule says may have changed,
			// which is exactly the entry the ledger exists to hold.
			kind = "normalized"
		}
		if score > best.Score && score >= threshold {
			best = pointMatch{Point: point, Kind: kind, Score: score}
		}
	}
	return best
}

// functionOf is a point's function and topic as comparable text.
func functionOf(point store.FunctionPoint) string {
	return normalizePoint(point.Function + point.Topic)
}

// pointRule is a point's current rule as comparable text.
func pointRule(point store.FunctionPoint) string {
	return normalizePoint(point.LatestRule)
}

// labelOf is a point's comparable text: its label, or the three parts.
func labelOf(point store.FunctionPoint) string {
	if strings.TrimSpace(point.Label) != "" {
		return normalizePoint(point.Label)
	}
	return normalizePoint(pointKey(point.Module, point.Function, point.Topic))
}

// pointKey joins a point's three parts as one comparable string.
func pointKey(module, function, topic string) string {
	return normalizePoint(module) + "|" + normalizePoint(function) + "|" + normalizePoint(topic)
}

// pointSimilarity is the Dice coefficient over the character bigrams of two
// comparable strings: it survives word order and a re-worded part, and it
// needs no model call.
func pointSimilarity(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	if len(ra) < 2 || len(rb) < 2 {
		return 0
	}
	grams := map[string]int{}
	for i := 0; i+1 < len(ra); i++ {
		grams[string(ra[i:i+2])]++
	}
	shared := 0
	for i := 0; i+1 < len(rb); i++ {
		gram := string(rb[i : i+2])
		if grams[gram] > 0 {
			grams[gram]--
			shared++
		}
	}
	return 2 * float64(shared) / float64(len(ra)-1+len(rb)-1)
}

// pointModifiers are the words a name picks up as a document is revised,
// which must not make a point look like a new one.
var pointModifiers = []string{
	"二期", "三期", "一期", "第二期", "补充", "续", "新版", "旧版",
}

// versionRE is a version or revision number: it says which document the
// rule came from, not which rule it is.
var versionRE = regexp.MustCompile(`v?\d+(\.\d+)*`)

// normalizePoint reduces a name to what identifies it: letters, digits and
// Chinese, lower case, no spacing, punctuation, revision wording or version
// numbers.
func normalizePoint(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 0x4e00 && r <= 0x9fff, r >= 0x3400 && r <= 0x4dbf:
			b.WriteRune(r)
		}
	}
	out := b.String()
	for _, modifier := range pointModifiers {
		out = strings.ReplaceAll(out, modifier, "")
	}
	return versionRE.ReplaceAllString(out, "")
}
