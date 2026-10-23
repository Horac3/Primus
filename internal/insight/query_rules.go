package insight

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// RulesInterpreter turns common history questions into a Filter with keyword
// and regex rules. It is the fallback when no model is configured or the model
// fails, so it never makes a network call.
type RulesInterpreter struct{}

const queryComparison = `(over|above|greater than|more than|exceeding|>=?|under|below|less than|<=?)`

var (
	quotedTargetPattern = regexp.MustCompile(`"([^"]+)"`)
	// A bare number ("error rate 5%", "error rate of 5%") is a minimum.
	errorRatePattern    = regexp.MustCompile(`\berror[- ]rate\s*(?:is\s+|of\s+)?` + queryComparison + `?\s*(\d+(?:\.\d+)?)\s*(?:%|percent\b)?`)
	errorRateWords      = regexp.MustCompile(`\berror[- ]rates?\b`)
	noErrorsPattern     = regexp.MustCompile(`\b(?:without errors|no errors|zero errors|error[- ]free)\b`)
	latencyPattern      = regexp.MustCompile(`\b(p50|p95|p99|median|latency)(?:\s+latency)?\s*(?:is\s+)?` + queryComparison + `\s*(\d+(?:\.\d+)?)\s*(ms|milliseconds|seconds|secs|sec|s)?\b`)
	countedWindow       = regexp.MustCompile(`\b(?:in\s+the\s+)?(?:last|past)\s+(\d+)\s+(minutes?|mins?|hours?|hrs?|days?|weeks?|wks?|months?)\b`)
	singleWindow        = regexp.MustCompile(`\b(?:in\s+the\s+)?(?:last|past|this)\s+(hour|day|week|month)\b`)
	todayPattern        = regexp.MustCompile(`\btoday\b`)
	yesterdayPattern    = regexp.MustCompile(`\byesterday(?:'s)?\b`)
	minRequestsPattern  = regexp.MustCompile(`\b(?:at least|over|more than|>=?)\s*(\d+)\s+requests\b`)
	limitBeforePattern  = regexp.MustCompile(`\b(?:top|last|latest|first|recent|show)\s+(\d+)\b`)
	limitAfterPattern   = regexp.MustCompile(`\b(\d+)\s+(?:runs|results|tests)\b`)
	mostErrorsPattern   = regexp.MustCompile(`\b(?:most errors|most failures|high(?:est)? error[- ]rates?|worst)\b`)
	noFailuresPattern   = regexp.MustCompile(`\b(?:no|without|zero) failures?\b`)
	notStoppedPattern   = regexp.MustCompile(`\bnot (?:stopped|cancell?ed|aborted)\b`)
	datePattern         = regexp.MustCompile(`^\d{4}-\d{2}`)
	slowestPattern      = regexp.MustCompile(`\bslowest\b`)
	fastestPattern      = regexp.MustCompile(`\b(?:fastest|quickest)\b`)
	oldestPattern       = regexp.MustCompile(`\b(?:oldest|earliest)\b`)
	newestPattern       = regexp.MustCompile(`\b(?:newest|latest|most recent)\b`)
	noAssertionsPattern = regexp.MustCompile(`\bno assertions?\b`)
	failPattern         = regexp.MustCompile(`\b(?:fail|failed|failing|failures?)\b`)
	passPattern         = regexp.MustCompile(`\b(?:pass|passed|passing|succeeded|successful)\b`)
	stoppedPattern      = regexp.MustCompile(`\b(?:stopped|cancell?ed|aborted)\b`)
	modePattern         = regexp.MustCompile(`\b(load|burst|soak|rate)\b`)
	targetPattern       = regexp.MustCompile(`\b(?:against|hitting|targeting|for|on)\s+(\S+)`)
)

var targetStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "my": true, "all": true, "me": true, "last": true, "past": true, "this": true,
	"runs": true, "tests": true, "mode": true, "with": true, "and": true, "of": true, "today": true, "yesterday": true, "load": true, "burst": true, "soak": true, "rate": true,
}

var windowHours = map[string]int{"hour": 1, "hr": 1, "day": 24, "week": 24 * 7, "wk": 24 * 7, "month": 24 * 30}

// Parse returns the filter for question and whether any rule matched. An
// unmatched question returns the default (all runs) filter and false.
func (RulesInterpreter) Parse(question string, now time.Time) (Filter, bool) {
	var f Filter
	text := question
	if match := quotedTargetPattern.FindStringSubmatch(text); match != nil {
		f.Target = match[1]
		text = strings.Replace(text, match[0], " ", 1)
	}
	text = strings.ToLower(text)
	take := func(pattern *regexp.Regexp) []string {
		match := pattern.FindStringSubmatch(text)
		if match != nil {
			text = strings.Replace(text, match[0], " ", 1)
		}
		return match
	}

	if match := take(errorRatePattern); match != nil {
		value, _ := strconv.ParseFloat(match[2], 64)
		if isUpperBound(match[1]) {
			f.MaxErrorRatePct = &value
		} else {
			f.MinErrorRatePct = &value
		}
	} else if take(noErrorsPattern) != nil {
		zero := 0.0
		f.MaxErrorRatePct = &zero
	}
	for match := take(latencyPattern); match != nil; match = take(latencyPattern) {
		value, _ := strconv.ParseFloat(match[3], 64)
		if unit := match[4]; unit != "" && unit != "ms" && unit != "milliseconds" {
			value *= 1000
		}
		millis := int(value)
		upper := isUpperBound(match[2])
		switch match[1] {
		case "p50", "median":
			setBound(&f.MinP50Millis, &f.MaxP50Millis, millis, upper)
		case "p99":
			setBound(&f.MinP99Millis, &f.MaxP99Millis, millis, upper)
		default:
			setBound(&f.MinP95Millis, &f.MaxP95Millis, millis, upper)
		}
	}

	if match := take(countedWindow); match != nil {
		count, _ := strconv.Atoi(match[1])
		if unit := strings.TrimSuffix(match[2], "s"); unit == "minute" || unit == "min" {
			// within_hours is whole hours, so a minute window rounds up.
			f.WithinHours = max(1, (count+59)/60)
		} else {
			f.WithinHours = count * windowHours[unit]
		}
	} else if match := take(singleWindow); match != nil {
		f.WithinHours = windowHours[match[1]]
	} else if take(todayPattern) != nil {
		f.Since = now.Format(queryDateLayout)
	} else if take(yesterdayPattern) != nil {
		day := now.AddDate(0, 0, -1).Format(queryDateLayout)
		f.Since, f.Until = day, day
	}
	if f.WithinHours > maxWithinHours {
		f.WithinHours = maxWithinHours
	}

	if match := take(minRequestsPattern); match != nil {
		f.MinTotalRequests, _ = strconv.Atoi(match[1])
	}
	if match := take(limitBeforePattern); match != nil {
		f.Limit, _ = strconv.Atoi(match[1])
	} else if match := take(limitAfterPattern); match != nil {
		f.Limit, _ = strconv.Atoi(match[1])
	}
	if f.Limit > maxQueryLimit {
		f.Limit = maxQueryLimit
	}

	switch {
	case take(mostErrorsPattern) != nil:
		f.Sort = SortErrorRateDesc
	case take(slowestPattern) != nil:
		f.Sort = SortP95Desc
	case take(fastestPattern) != nil:
		f.Sort = SortP95Asc
	case take(oldestPattern) != nil:
		f.Sort = SortOldest
	case take(newestPattern) != nil:
		f.Sort = SortNewest
	}

	switch {
	case take(noAssertionsPattern) != nil:
		f.Outcome = OutcomeGroupNoAssertions
	case take(noFailuresPattern) != nil:
		f.Outcome = OutcomeGroupPass
	case take(failPattern) != nil:
		f.Outcome = OutcomeGroupFail
	case take(passPattern) != nil:
		f.Outcome = OutcomeGroupPass
	}
	if take(notStoppedPattern) != nil {
		stopped := false
		f.Stopped = &stopped
	} else if take(stoppedPattern) != nil {
		stopped := true
		f.Stopped = &stopped
	}
	// Leftover "error rate" words must not read as rate mode.
	text = errorRateWords.ReplaceAllString(text, " ")
	for _, match := range modePattern.FindAllStringSubmatch(text, -1) {
		f.Modes = append(f.Modes, match[1])
	}
	text = modePattern.ReplaceAllString(text, " ")

	if f.Target == "" {
		for _, match := range targetPattern.FindAllStringSubmatch(text, -1) {
			word := strings.Trim(match[1], `?.,!;:'`)
			if _, err := strconv.Atoi(word); word == "" || err == nil || targetStopWords[word] || datePattern.MatchString(word) {
				continue
			}
			f.Target = word
			break
		}
	}

	empty, _ := Filter{}.Validate()
	validated, err := f.Validate()
	if err != nil {
		return empty, false
	}
	// An explicit "newest" sort matches the default but still counts as understood.
	return validated, f.Sort != "" || validated.Describe() != empty.Describe()
}

func isUpperBound(comparison string) bool {
	switch comparison {
	case "under", "below", "less than", "<", "<=":
		return true
	}
	return false
}

func setBound(min, max *int, value int, upper bool) {
	if upper {
		*max = value
	} else {
		*min = value
	}
}
