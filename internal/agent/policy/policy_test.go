package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Usir233/GENYEE/internal/types"
)

const (
	readOnly  = types.AssistantAccessReadOnly
	confirm   = types.AssistantAccessConfirm
	autoWrite = types.AssistantAccessAutoWrite
)

func TestDecide(t *testing.T) {
	want := map[Risk]map[types.AssistantAccessLevel]Verdict{
		RiskRead:        {readOnly: Allow, confirm: Allow, autoWrite: Allow},
		RiskProbe:       {readOnly: Allow, confirm: Allow, autoWrite: Allow},
		RiskWrite:       {readOnly: Deny, confirm: Ask, autoWrite: Allow},
		RiskDestructive: {readOnly: Deny, confirm: Ask, autoWrite: Ask},
	}
	for risk, byLevel := range want {
		for level, verdict := range byLevel {
			assert.Equal(t, verdict, Decide(Action{Risk: risk}, Rules{Level: level}), "%s at %s", risk, level)
			asked := Ask
			if verdict == Deny {
				asked = Deny
			}
			assert.Equal(t, asked, Decide(Action{Risk: risk, Always: true}, Rules{Level: level}),
				"always %s at %s", risk, level)
		}
	}
}

func TestDecideTreatsAnUnknownRiskAsDestructive(t *testing.T) {
	assert.Equal(t, Ask, Decide(Action{Risk: "unknown"}, Rules{Level: autoWrite}))
	assert.Equal(t, Deny, Decide(Action{Risk: "unknown"}, Rules{Level: readOnly}))
}

func TestRiskAbove(t *testing.T) {
	assert.True(t, RiskWrite.Above(RiskRead))
	assert.True(t, RiskRead.Above(""))
	assert.False(t, RiskRead.Above(RiskWrite))
	assert.False(t, RiskWrite.Above(RiskWrite))
}

func TestOffered(t *testing.T) {
	assert.True(t, Offered(readOnly, RiskRead))
	assert.True(t, Offered(readOnly, RiskProbe))
	assert.False(t, Offered(readOnly, RiskWrite))
	assert.False(t, Offered(readOnly, RiskDestructive))
	assert.True(t, Offered(confirm, RiskDestructive))
}

func TestPregrantableAndCovers(t *testing.T) {
	assert.True(t, Pregrantable(Action{Risk: RiskWrite}))
	assert.True(t, Pregrantable(Action{Risk: RiskDestructive}))
	assert.False(t, Pregrantable(Action{Risk: RiskDestructive, Always: true}))
	assert.False(t, Pregrantable(Action{Risk: RiskRead}))
	assert.False(t, Grantable(Action{Risk: RiskDestructive}), "a run's card never remembers a destructive call")

	assert.True(t, Covers(RiskDestructive, RiskWrite))
	assert.True(t, Covers(RiskWrite, RiskWrite))
	assert.False(t, Covers(RiskWrite, RiskDestructive))
	assert.False(t, Covers("", RiskWrite))
	assert.False(t, Covers(RiskDestructive, "bogus"))
}

func TestMalformedNonEmptyAccessLevelFailsClosed(t *testing.T) {
	for _, malformed := range []types.AssistantAccessLevel{"readonly", "none", "bogus", "ADMIN"} {
		for _, risk := range []Risk{RiskRead, RiskProbe, RiskWrite, RiskDestructive, "unknown"} {
			assert.False(t, Offered(malformed, risk), "level=%q risk=%q must not be offered", malformed, risk)
			assert.Equal(
				t,
				Deny,
				Decide(Action{Risk: risk},
					Rules{Level: malformed}),
				"level=%q risk=%q must deny",
				malformed,
				risk,
			)
		}
	}
	// Zero-value "" remains the default confirm level for callers omitting Rules.Level.
	assert.True(t, Offered("", RiskWrite))
	assert.Equal(t, Ask, Decide(Action{Risk: RiskWrite}, Rules{Level: ""}))
	assert.Equal(t, Allow, Decide(Action{Risk: RiskRead}, Rules{Level: ""}))

	// Malformed granted risk never covers any call.
	assert.False(t, Covers("bogus", RiskRead))
}
