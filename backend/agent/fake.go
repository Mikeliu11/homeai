package agent

import (
	"context"
	"fmt"
	"strings"
)

// ScriptedLLM is the local stand-in for Claude. It recognises the starter
// pack by keywords and file names and emits the same tool calls the real
// model should, so the whole UI can be exercised without AWS. Local dev only.
type ScriptedLLM struct{}

func (ScriptedLLM) Converse(_ context.Context, req ConverseRequest) (ConverseResponse, error) {
	last := req.Messages[len(req.Messages)-1]
	// after a tool result, finish the turn with a short reply
	for _, c := range last.Content {
		if c.ToolResult != nil {
			var lines []string
			for _, c := range last.Content {
				if c.ToolResult != nil {
					lines = append(lines, c.ToolResult.Text)
				}
			}
			return ConverseResponse{StopReason: "end_turn", Message: Message{Role: "assistant", Content: []ContentBlock{{Text: scriptedReply(lines)}}}}, nil
		}
	}
	text := strings.ToLower(joinText(last))
	helpers := map[string]string{} // name → id, parsed from the system prompt
	for _, l := range strings.Split(req.System, "\n") {
		if strings.HasPrefix(l, "- ") && strings.Contains(l, "(id ") {
			name := strings.TrimSpace(strings.SplitN(l[2:], " (id ", 2)[0])
			id := strings.SplitN(strings.SplitN(l, "(id ", 2)[1], ")", 2)[0]
			helpers[strings.ToLower(name)] = id
		}
	}
	srcIDs := sourceIDs(last)
	var tools []ContentBlock
	use := func(name string, in map[string]any) {
		tools = append(tools, ContentBlock{ToolUse: &ToolUse{ID: fmt.Sprintf("t%d", len(tools)+1), Name: name, Input: in}})
	}
	helperRef := func(name, role, pay string, days []any) map[string]any {
		if id, ok := helpers[strings.ToLower(name)]; ok {
			return map[string]any{"helperId": id}
		}
		return map[string]any{"newHelper": map[string]any{"name": name, "role": role, "payMethod": pay, "usualDays": days}}
	}
	merge := func(a, b map[string]any) map[string]any {
		for k, v := range b {
			a[k] = v
		}
		return a
	}
	// Every matching source makes its card, so several files in one turn each get one.
	if strings.Contains(text, "household-notes") || strings.Contains(text, "my partner is sam") {
		use("remember", map[string]any{"fact": "Jordan's partner is Sam. Kids: Maya (7) and Leo (4). Dog: Biscuit."})
		use("remember", map[string]any{"fact": "Everyone is paid at the start of the month for the previous month. Priya and Theo by Venmo, Lupe by Zelle."})
		use("remember", map[string]any{"fact": "Lupe has a key and comes Tuesday mornings. Priya usually sits Wednesdays after school plus some weekend nights. Kids are in bed by 8."})
		use("propose_terms", merge(helperRef("Theo", "dog walker", "Venmo", []any{"mon", "tue", "wed", "thu", "fri"}), map[string]any{
			"effectiveFrom": "", "defaultPerVisit": "walk",
			"rules":     []any{map[string]any{"id": "r1", "kind": "per_unit", "service": "walk", "label": "$25 a walk", "unit": "walk", "amountCents": 2500}},
			"sourceIds": srcIDs, "summary": "From Jordan's notes: $25 a walk, weekdays around noon; sometimes a long one (price not stated).",
			"assumptions": []any{"Jordan's note is undated, so these terms have no effective date and count only until a dated message says otherwise.", "The long walk price is not in the note."}}))
	}
	if strings.Contains(text, "priya-rates") || strings.Contains(text, "$22/hr") {
		use("propose_terms", merge(helperRef("Priya", "babysitter", "Venmo", []any{"wed", "sat", "sun"}), map[string]any{
			"effectiveFrom": "2026-06-14",
			"rules": []any{
				map[string]any{"id": "r1", "kind": "hourly", "service": "hourly", "label": "$22/hr", "rateCents": 2200},
				map[string]any{"id": "r2", "kind": "hourly", "service": "hourly", "label": "after 10pm $28/hr", "rateCents": 2800, "when": map[string]any{"from": "22:00"}},
				map[string]any{"id": "r3", "kind": "multiplier", "label": "Sundays time and a half", "factor": 1.5, "when": map[string]any{"days": []any{"sun"}}},
				map[string]any{"id": "r4", "kind": "flat", "service": "date_night", "label": "date night flat $100 until 11", "amountCents": 10000, "until": "23:00"}},
			"sourceIds": srcIDs, "summary": "$22/hr, $28/hr after 10pm, Sundays time and a half; date nights a flat $100 until 11pm, then the late rate.",
			"assumptions": []any{"The thread is dated Sun, Jun 14; the year is taken as 2026.", "Date night start time is not stated; the flat covers until 23:00."}}))
	}
	if strings.Contains(text, "03-lupe") || strings.Contains(text, "carmen gave me your number") {
		use("propose_terms", merge(helperRef("Lupe", "cleaner", "Zelle", []any{"tue"}), map[string]any{
			"effectiveFrom": "2026-01-14", "defaultPerVisit": "regular_visit",
			"rules": []any{
				map[string]any{"id": "r1", "kind": "per_unit", "service": "regular_visit", "label": "$140 each visit", "unit": "visit", "amountCents": 14000},
				map[string]any{"id": "r2", "kind": "per_unit", "service": "deep_clean", "label": "deep clean (oven, fridge, baseboards) $220", "unit": "visit", "amountCents": 22000}},
			"sourceIds": srcIDs, "summary": "Weekly Tuesday visit $140; deep clean $220.",
			"assumptions": []any{"Thread dated Wed, Jan 14 (2026). Visits started the following Tuesday, Jan 20."}}))
	}
	if strings.Contains(text, "starting the 15th") {
		use("propose_terms", merge(helperRef("Lupe", "cleaner", "Zelle", []any{"tue"}), map[string]any{
			"effectiveFrom": "2026-09-15", "defaultPerVisit": "regular_visit",
			"rules": []any{
				map[string]any{"id": "r1", "kind": "per_unit", "service": "regular_visit", "label": "regular visit $150 from the 15th", "unit": "visit", "amountCents": 15000},
				map[string]any{"id": "r2", "kind": "per_unit", "service": "deep_clean", "label": "deep clean stays $220", "unit": "visit", "amountCents": 22000}},
			"sourceIds": srcIDs, "summary": "Regular visit goes to $150 from Sep 15; deep clean stays $220. Deep clean planned for Sep 22.",
			"diff":        []any{"regular visit: $140 → $150"},
			"assumptions": []any{"\"Starting the 15th\" in a message dated Aug 28 is read as September 15, inclusive."}}))
	}
	if strings.Contains(text, "theo-rates") || strings.Contains(text, "$28 for a 30-min walk") {
		use("propose_terms", merge(helperRef("Theo", "dog walker", "Venmo", []any{"mon", "tue", "wed", "thu", "fri"}), map[string]any{
			"effectiveFrom": "2026-09-01", "defaultPerVisit": "walk_30",
			"rules": []any{
				map[string]any{"id": "r1", "kind": "per_unit", "service": "walk_30", "label": "$28 for a 30-min walk", "unit": "walk", "amountCents": 2800},
				map[string]any{"id": "r2", "kind": "per_unit", "service": "walk_60", "label": "$38 for an hour", "unit": "walk", "amountCents": 3800}},
			"sourceIds": srcIDs, "summary": "From September: $28 per 30-minute walk, $38 per hour walk.",
			"conflicts":   []any{"Jordan's note says $25 a walk; Theo's Aug 27 text says $28/$38 from September. Using the dated text for September onward."},
			"assumptions": []any{"\"September onward\" = effective 2026-09-01."}}))
	}
	if strings.Contains(text, "here's september") || strings.Contains(text, "date night 6") {
		use("propose_bill", merge(helperRef("Priya", "babysitter", "Venmo", []any{"wed", "sat", "sun"}), map[string]any{
			"period": "2026-09", "claimedTotalCents": 62600, "sourceIds": srcIDs, "summary": "Priya, September: 5 sits plus a $40 last-minute fee, total $626.",
			"lines": []any{
				map[string]any{"date": "2026-09-04", "service": "date_night", "start": "18:00", "end": "23:30", "claimedCents": 11400, "note": "Fri 9/4 date night 6–11:30pm — $114"},
				map[string]any{"date": "2026-09-09", "service": "hourly", "start": "15:00", "end": "19:00", "claimedCents": 8800, "note": "Wed 9/9 after school 3–7pm — $88"},
				map[string]any{"date": "2026-09-12", "service": "hourly", "start": "19:00", "end": "24:00", "claimedCents": 12200, "note": "Sat 9/12 7pm–12am — $122"},
				map[string]any{"date": "2026-09-20", "service": "hourly", "start": "10:00", "end": "14:00", "claimedCents": 13200, "note": "Sun 9/20 10am–2pm — $132"},
				map[string]any{"date": "2026-09-20", "service": "last_minute_fee", "claimedCents": 4000, "note": "+ last minute fee $40 (Sam said that was fine since it was same-day!)", "flags": map[string]any{"same_day": true}},
				map[string]any{"date": "2026-09-25", "service": "date_night", "start": "18:00", "end": "23:00", "claimedCents": 10000, "note": "Fri 9/25 date night 6–11pm — $100"}},
			"assumptions": []any{"Sat 9/12 is not labelled a date night, so it is read as hourly.", "The $40 fee is kept as its own line; Sam's say-so is not a confirmed agreement."}}))
	}
	if strings.Contains(text, "lupe-september-bill") || strings.Contains(text, "sep 22 deep") {
		use("propose_bill", merge(helperRef("Lupe", "cleaner", "Zelle", []any{"tue"}), map[string]any{
			"period": "2026-09", "claimedTotalCents": 82000, "sourceIds": srcIDs, "summary": "Lupe, September: four regular visits at $150 and a deep clean, total $820.",
			"lines": []any{
				map[string]any{"date": "2026-09-01", "service": "regular_visit", "claimedCents": 15000, "note": "Sep 1 – $150"},
				map[string]any{"date": "2026-09-08", "service": "regular_visit", "claimedCents": 15000, "note": "Sep 8 – $150"},
				map[string]any{"date": "2026-09-15", "service": "regular_visit", "claimedCents": 15000, "note": "Sep 15 – $150"},
				map[string]any{"date": "2026-09-22", "service": "deep_clean", "claimedCents": 22000, "note": "Sep 22 deep – $220"},
				map[string]any{"date": "2026-09-29", "service": "regular_visit", "claimedCents": 15000, "note": "Sep 29 – $150"}}}))
	}
	if strings.Contains(text, "september walks") || strings.Contains(text, "30-min walks: 18") {
		use("propose_bill", merge(helperRef("Theo", "dog walker", "Venmo", []any{"mon", "tue", "wed", "thu", "fri"}), map[string]any{
			"period": "2026-09", "claimedTotalCents": 58000, "sourceIds": srcIDs, "summary": "Theo, September: 18 half-hour walks and 2 hour walks, total $580. No walk on Labor Day.",
			"lines": []any{
				map[string]any{"service": "walk_30", "qty": 18, "claimedCents": 50400, "note": "30-min walks: 18 x $28 = $504"},
				map[string]any{"service": "walk_60", "qty": 2, "claimedCents": 7600, "note": "60-min walks: 2 x $38 = $76"}}}))
	}
	if strings.Contains(text, "remember") {
		use("remember", map[string]any{"fact": strings.TrimSpace(joinText(last))})
	}
	if len(tools) == 0 {
		return ConverseResponse{StopReason: "end_turn", Message: Message{Role: "assistant", Content: []ContentBlock{{Text: "(local scripted assistant) I didn't recognise that. Paste one of the starter-pack files, or ask me to remember something."}}}}, nil
	}
	return ConverseResponse{StopReason: "tool_use", Message: Message{Role: "assistant", Content: tools}}, nil
}

func scriptedReply(results []string) string {
	var sb strings.Builder
	sb.WriteString("(local scripted assistant) ")
	terms, remembered := 0, false
	for _, r := range results {
		switch {
		case strings.HasPrefix(r, "Bill proposal"):
			headline := r
			if i := strings.Index(r, "recompute): "); i >= 0 {
				headline = r[i+len("recompute): "):]
			}
			if i := strings.Index(headline, ". "); i >= 0 {
				headline = headline[:i]
			}
			sb.WriteString("I've laid out the bill as line items. The engine says: " + strings.TrimSuffix(strings.TrimSpace(headline), ".") + ". Confirm the card and you'll get the full check. ")
		case strings.HasPrefix(r, "Proposal"):
			terms++
		case r == "Remembered.":
			remembered = true
		case strings.HasPrefix(r, "Error"):
			sb.WriteString(r + " ")
		}
	}
	switch terms {
	case 0:
	case 1:
		sb.WriteString("I've drafted the terms from what you pasted; confirm the card if it matches what you agreed. ")
	default:
		sb.WriteString(fmt.Sprintf("I've drafted terms for %d helpers, one card each; confirm the ones that match what you agreed. ", terms))
	}
	if remembered {
		sb.WriteString("Noted for next time. ")
	}
	return strings.TrimSpace(sb.String())
}

func joinText(m Message) string {
	var parts []string
	for _, c := range m.Content {
		if c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func sourceIDs(m Message) []any {
	var out []any
	for _, c := range m.Content {
		if strings.HasPrefix(c.Text, "[Source ") {
			id := strings.SplitN(strings.TrimPrefix(c.Text, "[Source "), ",", 2)[0]
			out = append(out, id)
		}
	}
	return out
}
