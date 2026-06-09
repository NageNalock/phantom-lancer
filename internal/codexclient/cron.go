package codexclient

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// cronSchedule models a standard 5-field cron expression
// (minute hour day-of-month month day-of-week) interpreted in UTC. It supports
// `*`, comma lists, ranges (`a-b`) and steps (`*/n`, `a-b/n`). It is intentionally
// minimal: no named months/weekdays, no `@` macros, no second field. Automations
// only need periodic wakeups, so this avoids a third-party dependency.
type cronSchedule struct {
	minutes []bool // 0-59
	hours   []bool // 0-23
	dom     []bool // 1-31
	months  []bool // 1-12
	dow     []bool // 0-6 (Sunday = 0)
	domStar bool
	dowStar bool
}

// validateCronExpr reports whether expr is a parseable 5-field cron expression.
func validateCronExpr(expr string) error {
	_, err := parseCron(expr)
	return err
}

func parseCron(expr string) (cronSchedule, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return cronSchedule{}, errors.New("cron expression must have 5 fields")
	}
	minutes, _, err := parseCronField(fields[0], 0, 59)
	if err != nil {
		return cronSchedule{}, err
	}
	hours, _, err := parseCronField(fields[1], 0, 23)
	if err != nil {
		return cronSchedule{}, err
	}
	dom, domStar, err := parseCronField(fields[2], 1, 31)
	if err != nil {
		return cronSchedule{}, err
	}
	months, _, err := parseCronField(fields[3], 1, 12)
	if err != nil {
		return cronSchedule{}, err
	}
	dowRaw, dowStar, err := parseCronField(fields[4], 0, 7)
	if err != nil {
		return cronSchedule{}, err
	}
	if dowRaw[7] {
		dowRaw[0] = true
	}
	dow := dowRaw[:7]
	return cronSchedule{minutes: minutes, hours: hours, dom: dom, months: months, dow: dow, domStar: domStar, dowStar: dowStar}, nil
}

// parseCronField expands one cron field into a presence slice indexed from min.
// The returned bool reports whether the field was an unrestricted `*`.
func parseCronField(field string, min, max int) ([]bool, bool, error) {
	size := max - min + 1
	out := make([]bool, size)
	isStar := false
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false, errors.New("cron field has an empty term")
		}
		step := 1
		rangePart := part
		if slash := strings.Index(part, "/"); slash >= 0 {
			rangePart = part[:slash]
			stepValue, err := strconv.Atoi(part[slash+1:])
			if err != nil || stepValue <= 0 {
				return nil, false, errors.New("cron step must be a positive integer")
			}
			step = stepValue
		}
		lo, hi := min, max
		if rangePart == "*" {
			isStar = true
		} else if dash := strings.Index(rangePart, "-"); dash >= 0 {
			loValue, err := strconv.Atoi(rangePart[:dash])
			if err != nil {
				return nil, false, errors.New("invalid cron range start")
			}
			hiValue, err := strconv.Atoi(rangePart[dash+1:])
			if err != nil {
				return nil, false, errors.New("invalid cron range end")
			}
			lo, hi = loValue, hiValue
		} else {
			value, err := strconv.Atoi(rangePart)
			if err != nil {
				return nil, false, errors.New("invalid cron value")
			}
			lo, hi = value, value
		}
		if lo < min || hi > max || lo > hi {
			return nil, false, errors.New("cron value out of range")
		}
		for v := lo; v <= hi; v += step {
			out[v-min] = true
		}
	}
	return out, isStar, nil
}

func (c cronSchedule) matches(t time.Time) bool {
	if !c.minutes[t.Minute()] || !c.hours[t.Hour()] || !c.months[int(t.Month())-1] {
		return false
	}
	domMatch := c.dom[t.Day()-1]
	dowMatch := c.dow[int(t.Weekday())]
	// Standard cron semantics: when both day-of-month and day-of-week are
	// restricted, a tick matches if either matches; otherwise both must match.
	if !c.domStar && !c.dowStar {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

// nextCronTime returns the next minute strictly after `from` that matches expr.
// It searches up to roughly four years ahead before giving up.
func nextCronTime(expr string, from time.Time) (time.Time, bool) {
	schedule, err := parseCron(expr)
	if err != nil {
		return time.Time{}, false
	}
	candidate := from.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := candidate.Add(4 * 366 * 24 * time.Hour)
	for candidate.Before(limit) {
		if schedule.matches(candidate) {
			return candidate, true
		}
		candidate = candidate.Add(time.Minute)
	}
	return time.Time{}, false
}
