package ingest

import (
	"strconv"
	"strings"
)

// VersionRank orders the versions a document carries, so that the entries of
// a change log and the documents of a ledger read from the older to the newer
// one: 1.0.0 → 1000, 1.1.0 → 1010, 2.0 → 2000, 1.0.1 → 1001. A value that is
// not a version — a date, as 2023.12 or 24.04.29, which the team also writes
// where a version would go, or an empty cell — is -1.
func VersionRank(version string) int {
	s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(version), "Vv"))
	if s == "" {
		return -1
	}
	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return -1
	}
	nums := make([]int, 0, 3)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return -1
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return -1
			}
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return -1
		}
		nums = append(nums, n)
	}
	// A first part of four digits is a year (2023.12), and a first part of
	// two digits whose second and third parts read as a month and a day is a
	// date written without its century (24.04.29). Neither is a version.
	if nums[0] > 99 {
		return -1
	}
	if len(nums) >= 2 && len(parts[0]) == 2 && nums[0] >= 20 && nums[0] <= 40 &&
		nums[1] >= 1 && nums[1] <= 12 && (len(nums) == 2 || (nums[2] >= 1 && nums[2] <= 31)) {
		return -1
	}
	rank := nums[0] * 1000
	if len(nums) > 1 {
		rank += nums[1] * 10
	}
	if len(nums) > 2 {
		rank += nums[2]
	}
	return rank
}
