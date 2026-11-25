package core

import (
	"fmt"
	"testing"
	"time"
)

func TestAGAccountTimelineSingleAccountPassthrough(t *testing.T) {
	var tl AGAccountTimeline
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

	changed, switched := tl.Observe("UserA@Example.com ", t0)
	if !changed || switched {
		t.Fatalf("expected changed=true switched=false on first observe, got changed=%v switched=%v", changed, switched)
	}
	changed2, switched2 := tl.Observe("usera@example.com", t0.Add(5*time.Minute))
	if changed2 || switched2 {
		t.Fatalf("expected no-op on same account, got changed=%v switched=%v", changed2, switched2)
	}
	if tl.HasMultipleAccounts() {
		t.Fatal("expected HasMultipleAccounts=false before any switch")
	}

	recs := []AGTranscriptRecord{
		{Timestamp: t0.Add(-2 * time.Hour), ConversationID: "c1", OutputTokens: 100},
		{Timestamp: t0.Add(1 * time.Minute), ConversationID: "c2", OutputTokens: 200},
	}
	got, convs, filtered := tl.FilterRecords(recs, "usera@example.com", false)
	if filtered {
		t.Fatal("expected single-account timeline not to filter pre-tracking history")
	}
	if len(got) != 2 || convs != 2 {
		t.Fatalf("expected all 2 records/convs preserved, got %d recs / %d convs", len(got), convs)
	}
}

func TestAGAccountTimelineSwitchAndSwitchBackIsolation(t *testing.T) {
	var tl AGAccountTimeline
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(30 * time.Minute)
	t2 := t0.Add(60 * time.Minute)

	tl.Observe("alice@gmail.com", t0)
	changed, switched := tl.Observe("Bob@gmail.com", t1)
	if !changed || !switched {
		t.Fatalf("expected switch on Alice->Bob, got changed=%v switched=%v", changed, switched)
	}
	if !tl.HasMultipleAccounts() {
		t.Fatal("expected HasMultipleAccounts=true after switch")
	}

	preHistory := AGTranscriptRecord{Timestamp: t0.Add(-10 * time.Minute), ConversationID: "c0", OutputTokens: 50}
	aliceRec1 := AGTranscriptRecord{Timestamp: t0.Add(10 * time.Minute), ConversationID: "c1", OutputTokens: 100}
	bobRec := AGTranscriptRecord{Timestamp: t1.Add(10 * time.Minute), ConversationID: "c1", OutputTokens: 200}
	recs := []AGTranscriptRecord{preHistory, aliceRec1, bobRec}

	gotBob, convBob, filtBob := tl.FilterRecords(recs, "bob@gmail.com", false)
	if !filtBob || len(gotBob) != 1 || convBob != 1 || gotBob[0].OutputTokens != 200 {
		t.Fatalf("expected Bob to see only 1 record (200 out), got len=%d conv=%d recs=%+v", len(gotBob), convBob, gotBob)
	}

	// Empty right after switching to Carol: Carol has 0 records even though shared conversation exists.
	gotCarol, convCarol, filtCarol := tl.FilterRecords(recs, "bob@gmail.com", true)
	if filtCarol || len(gotCarol) != 3 || convCarol != 2 {
		t.Fatalf("expected includeAll=true to return all 3 recs / 2 convs, got %d / %d (filtered=%v)", len(gotCarol), convCarol, filtCarol)
	}

	// Switch back to Alice at t2 and add another record for Alice
	tl.Observe("ALICE@gmail.com", t2)
	aliceRec2 := AGTranscriptRecord{Timestamp: t2.Add(5 * time.Minute), ConversationID: "c2", OutputTokens: 300}
	recs = append(recs, aliceRec2)

	gotAlice, convAlice, filtAlice := tl.FilterRecords(recs, "alice@gmail.com", false)
	if !filtAlice || len(gotAlice) != 2 || convAlice != 2 {
		t.Fatalf("expected Alice to see her 2 segments (not Bob's or preHistory), got %d recs / %d convs", len(gotAlice), convAlice)
	}
	if gotAlice[0].OutputTokens != 100 || gotAlice[1].OutputTokens != 300 {
		t.Fatalf("unexpected Alice records: %+v", gotAlice)
	}
}

func TestAGAccountTimelineOutOfOrderClampAndPrune(t *testing.T) {
	var tl AGAccountTimeline
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	tl.Observe("a@example.com", base.Add(10*time.Minute))
	// Out-of-order earlier timestamp clamps to previous Start so segments never invert.
	tl.Observe("b@example.com", base.Add(2*time.Minute))
	if tl.Segments[0].End == nil || !tl.Segments[0].End.Equal(tl.Segments[0].Start) {
		t.Fatalf("expected out-of-order switch to clamp End to prev Start, got seg0=%+v", tl.Segments[0])
	}
	if !tl.Segments[1].Start.Equal(tl.Segments[0].Start) {
		t.Fatalf("expected out-of-order new segment Start clamped to prev Start, got seg1=%+v", tl.Segments[1])
	}

	// Add 60 old switches > 60 days before latest, then switch recently -> keeps >= 50 segments and HasSwitched=true.
	cur := base
	for i := 0; i < 60; i++ {
		cur = cur.Add(time.Hour)
		tl.Observe(fmt.Sprintf("u%d@example.com", i%2), cur)
	}
	tl.Observe("final@example.com", base.Add(120*24*time.Hour))
	if !tl.HasMultipleAccounts() {
		t.Fatal("expected HasMultipleAccounts=true preserved after pruning")
	}
	if len(tl.Segments) < agTimelineMinKeep {
		t.Fatalf("expected at least %d segments kept after prune, got %d", agTimelineMinKeep, len(tl.Segments))
	}
	if got := tl.AccountAt(base.Add(121 * 24 * time.Hour)); got != "final@example.com" {
		t.Fatalf("expected latest open segment active, got %q", got)
	}
}
