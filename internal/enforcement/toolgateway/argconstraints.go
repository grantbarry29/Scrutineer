/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// argIndexRE matches a trailing array index segment, e.g. "args[0]".
var argIndexRE = regexp.MustCompile(`^(.*)\[(\d+)\]$`)

// evaluateArgumentRules applies argument constraints to a request that has already passed
// name-level allow/deny. It returns the deny reason ("" when allowed) and, on a deny, the
// matched constraint (redacted, policy-defined fields only).
//
// Semantics (see docs/design/phase-3-tool-argument-constraints.md):
//   - Only rules whose Tools (or "*") and optional Server match the request apply.
//   - Within a matching rule, any Deny-effect constraint that matches denies the call.
//   - Allow-effect constraints form an allowlist: if a rule has ≥1 Allow constraint and
//     none match, the call is denied (ArgumentNotAllowed).
//   - Across rules, the first deny wins; constraints can only tighten.
func evaluateArgumentRules(rules []scrutineerv1alpha1.ToolArgumentRule, req ToolRequest) (string, *ArgConstraintMatch) {
	for i := range rules {
		rule := rules[i]
		if !ruleApplies(rule, req) {
			continue
		}

		var allow []scrutineerv1alpha1.ArgumentConstraint
		for j := range rule.Constraints {
			c := rule.Constraints[j]
			if c.Effect == scrutineerv1alpha1.ConstraintEffectAllow {
				allow = append(allow, c)
				continue
			}
			// Deny effect (default): a match blocks the call.
			if constraintMatches(c, req.Arguments) {
				return ReasonArgumentDenied, matchFromConstraint(c)
			}
		}

		if len(allow) > 0 && !anyConstraintMatches(allow, req.Arguments) {
			return ReasonArgumentNotAllowed, matchFromConstraint(allow[0])
		}
	}
	return "", nil
}

func ruleApplies(rule scrutineerv1alpha1.ToolArgumentRule, req ToolRequest) bool {
	if rule.Server != "" && rule.Server != req.Server {
		return false
	}
	for _, t := range rule.Tools {
		if t == "*" || t == req.Tool {
			return true
		}
	}
	return false
}

func anyConstraintMatches(cs []scrutineerv1alpha1.ArgumentConstraint, args map[string]any) bool {
	for i := range cs {
		if constraintMatches(cs[i], args) {
			return true
		}
	}
	return false
}

// constraintMatches reports whether the constraint's condition holds for the request
// arguments. Missing arguments only satisfy Exists/NotExists; for all value operators a
// missing argument is a non-match (avoids "absent arg denies everything" footguns).
func constraintMatches(c scrutineerv1alpha1.ArgumentConstraint, args map[string]any) bool {
	value, present := resolveArg(args, c.Arg)

	switch c.Op {
	case scrutineerv1alpha1.ArgOpExists:
		return present
	case scrutineerv1alpha1.ArgOpNotExists:
		return !present
	}

	if !present {
		return false
	}

	switch c.Op {
	case scrutineerv1alpha1.ArgOpEquals, scrutineerv1alpha1.ArgOpIn:
		return containsValue(c.Values, value)
	case scrutineerv1alpha1.ArgOpNotEquals, scrutineerv1alpha1.ArgOpNotIn:
		return !containsValue(c.Values, value)
	case scrutineerv1alpha1.ArgOpHasPrefix:
		return anyPrefix(c.Values, value)
	case scrutineerv1alpha1.ArgOpNotHasPrefix:
		return !anyPrefix(c.Values, value)
	case scrutineerv1alpha1.ArgOpMatches:
		return anyRegex(c.Values, value)
	case scrutineerv1alpha1.ArgOpNotMatches:
		return !anyRegex(c.Values, value)
	default:
		return false
	}
}

func containsValue(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func anyPrefix(values []string, v string) bool {
	for _, x := range values {
		if strings.HasPrefix(v, x) {
			return true
		}
	}
	return false
}

func anyRegex(values []string, v string) bool {
	for _, x := range values {
		re, err := regexp.Compile(x)
		if err != nil {
			continue
		}
		if re.MatchString(v) {
			return true
		}
	}
	return false
}

// resolveArg walks a dotted path with optional [index] segments (e.g. "options.path",
// "args[0]") into the decoded argument object, returning the value rendered as a string
// and whether the path was present.
func resolveArg(args map[string]any, path string) (string, bool) {
	if args == nil || path == "" {
		return "", false
	}
	var current any = args
	for _, segment := range strings.Split(path, ".") {
		key := segment
		var index = -1
		if m := argIndexRE.FindStringSubmatch(segment); m != nil {
			key = m[1]
			index, _ = strconv.Atoi(m[2])
		}

		if key != "" {
			m, ok := current.(map[string]any)
			if !ok {
				return "", false
			}
			current, ok = m[key]
			if !ok {
				return "", false
			}
		}

		if index >= 0 {
			arr, ok := current.([]any)
			if !ok || index >= len(arr) {
				return "", false
			}
			current = arr[index]
		}
	}
	return stringifyArg(current)
}

// stringifyArg renders a scalar JSON value as a string for comparison. Non-scalars
// (objects/arrays/null) are treated as absent so constraints target leaf values.
func stringifyArg(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case int:
		return strconv.Itoa(t), true
	case int64:
		return strconv.FormatInt(t, 10), true
	case json.Number:
		return t.String(), true
	default:
		return "", false
	}
}

func matchFromConstraint(c scrutineerv1alpha1.ArgumentConstraint) *ArgConstraintMatch {
	effect := c.Effect
	if effect == "" {
		effect = scrutineerv1alpha1.ConstraintEffectDeny
	}
	return &ArgConstraintMatch{
		Arg:          c.Arg,
		Op:           c.Op,
		Effect:       effect,
		PolicyValues: append([]string(nil), c.Values...),
	}
}

// argMatchDetail renders a redacted, human-readable description of a matched constraint
// (policy operands only; never the request value).
func argMatchDetail(m *ArgConstraintMatch) string {
	if m == nil {
		return ""
	}
	if len(m.PolicyValues) == 0 {
		return fmt.Sprintf("arg %q %s", m.Arg, m.Op)
	}
	return fmt.Sprintf("arg %q %s %v", m.Arg, m.Op, m.PolicyValues)
}
