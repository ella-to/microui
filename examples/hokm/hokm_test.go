package main

import (
	"slices"
	"testing"
	"time"

	miniui "ella.to/microui"
	"ella.to/microui/pkg/renderer"
)

func TestBeats(t *testing.T) {
	const trump, led = spades, hearts
	cases := []struct {
		name string
		a, b Card
		want bool
	}{
		{"trump beats non-trump", Card{spades, 2}, Card{hearts, 14}, true},
		{"non-trump loses to trump", Card{hearts, 14}, Card{spades, 2}, false},
		{"higher trump wins", Card{spades, 10}, Card{spades, 9}, true},
		{"higher led suit wins", Card{hearts, 13}, Card{hearts, 12}, true},
		{"off-suit cannot beat led", Card{diamonds, 14}, Card{hearts, 2}, false},
		{"led beats off-suit", Card{hearts, 2}, Card{diamonds, 14}, true},
	}
	for _, c := range cases {
		if got := beats(c.a, c.b, trump, led); got != c.want {
			t.Errorf("%s: beats(%v,%v)=%v, want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}

// playTrick sets up a complete trick led by leader with the given cards and
// returns the winning seat.
func trickWinner(trump Suit, leader int, cards [4]Card) int {
	g := &Game{trump: trump, leader: leader}
	for s := range 4 {
		g.trick[s] = cards[s]
		g.played[s] = true
	}
	w, _ := g.currentBest()
	return w
}

func TestTrickWinner(t *testing.T) {
	// Hearts led, spades trump. seatLeft trumps in and wins despite a low card.
	cards := [4]Card{
		seatYou:     {hearts, 14}, // led an Ace of hearts
		seatRight:   {hearts, 5},
		seatPartner: {diamonds, 13}, // off-suit, useless
		seatLeft:    {spades, 2},    // trumps in
	}
	if w := trickWinner(spades, seatYou, cards); w != seatLeft {
		t.Fatalf("expected seatLeft to win by trumping, got %d", w)
	}

	// No trumps played: highest of the led suit wins.
	cards = [4]Card{
		seatYou:     {clubs, 9},
		seatRight:   {clubs, 14}, // highest club
		seatPartner: {hearts, 13},
		seatLeft:    {clubs, 3},
	}
	if w := trickWinner(spades, seatYou, cards); w != seatRight {
		t.Fatalf("expected seatRight to win with A♣, got %d", w)
	}
}

func TestLegal(t *testing.T) {
	g := &Game{trump: spades, leader: seatRight}
	g.hands[seatYou] = []Card{{hearts, 5}, {hearts, 9}, {spades, 14}, {clubs, 2}}

	// Leading: every card is legal.
	if got := len(g.legal(seatYou)); got != 4 {
		t.Fatalf("leading should allow all 4 cards, got %d", got)
	}

	// Hearts led and we hold hearts: only the two hearts are legal.
	g.trick[seatRight] = Card{hearts, 13}
	g.played[seatRight] = true
	legal := g.legal(seatYou)
	if len(legal) != 2 {
		t.Fatalf("must follow hearts: expected 2 legal cards, got %d (%v)", len(legal), legal)
	}
	for _, i := range legal {
		if g.hands[seatYou][i].Suit != hearts {
			t.Fatalf("legal index %d is not a heart: %v", i, g.hands[seatYou][i])
		}
	}

	// Void in the led suit: anything goes.
	g.hands[seatYou] = []Card{{spades, 14}, {clubs, 2}, {diamonds, 7}}
	if got := len(g.legal(seatYou)); got != 3 {
		t.Fatalf("void in hearts should allow all 3 cards, got %d", got)
	}
}

func TestScoringAndHakemRotation(t *testing.T) {
	cases := []struct {
		name          string
		hakem         int
		you, opp      int // tricks taken this hand
		wantTeam      int // team that scores
		wantPts       int
		wantHakemKept bool
	}{
		{"hakem team wins normally", seatYou, 7, 5, 0, 1, true},
		{"hakem team sweeps (kot)", seatYou, 7, 0, 0, 2, true},
		{"opponents win normally", seatYou, 4, 7, 1, 1, false},
		{"opponents sweep the hakem", seatYou, 0, 7, 1, 3, false},
		{"hakem on opp team retains on win", seatRight, 3, 7, 1, 1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := &Game{hakem: c.hakem}
			g.tricksWon = [2]int{c.you, c.opp}
			g.endHand()
			if g.scores[c.wantTeam] != c.wantPts {
				t.Errorf("team %d scored %d, want %d", c.wantTeam, g.scores[c.wantTeam], c.wantPts)
			}
			kept := g.hakem == c.hakem
			if kept != c.wantHakemKept {
				t.Errorf("hakem kept=%v (now seat %d), want kept=%v", kept, g.hakem, c.wantHakemKept)
			}
			if g.phase != phaseHandDone {
				t.Errorf("phase=%d, want phaseHandDone", g.phase)
			}
		})
	}
}

func TestGameOver(t *testing.T) {
	g := &Game{hakem: seatYou}
	g.scores = [2]int{6, 0}
	g.tricksWon = [2]int{7, 2} // your team wins the hand, reaching 7 points
	g.endHand()
	if g.phase != phaseGameOver {
		t.Fatalf("expected phaseGameOver at 7 points, got phase %d", g.phase)
	}
	if g.scores[0] != 7 {
		t.Fatalf("expected score 7, got %d", g.scores[0])
	}
}

func TestAIPickTrumpFavorsLongestSuit(t *testing.T) {
	five := []Card{{clubs, 2}, {clubs, 5}, {clubs, 9}, {hearts, 14}, {spades, 13}}
	if got := aiPickTrump(five); got != clubs {
		t.Fatalf("aiPickTrump=%v, want clubs (longest suit)", got)
	}
}

// TestFullHandPlaythrough deals and plays out many random hands end-to-end,
// driving every seat (the human plays its first legal card, the AIs use their
// own logic). It asserts no seat ever plays an illegal card, every hand
// converges, and a winner is scored — exercising the whole engine over many
// random deals.
func TestFullHandPlaythrough(t *testing.T) {
	for game := range 300 {
		g := &Game{hakem: game % 4}
		g.newHand()
		g.setTrump(aiPickTrump(g.firstFive))

		for guard := 0; g.phase == phasePlaying || g.phase == phaseTrickDone; guard++ {
			if guard > 1000 {
				t.Fatalf("hand %d did not converge", game)
			}
			switch g.phase {
			case phasePlaying:
				seat := g.turn
				legal := g.legal(seat)
				idx := legal[0] // human: first legal card
				if seat != seatYou {
					idx = g.aiPlay(seat)
				}
				if !slices.Contains(legal, idx) {
					t.Fatalf("seat %d played illegal index %d (legal=%v)", seat, idx, legal)
				}
				g.play(seat, idx)
			case phaseTrickDone:
				g.resolveTrick()
			}
		}

		if g.phase != phaseHandDone && g.phase != phaseGameOver {
			t.Fatalf("hand %d ended in unexpected phase %d", game, g.phase)
		}
		if g.tricksWon[0] < 7 && g.tricksWon[1] < 7 {
			t.Fatalf("hand %d ended without a 7-trick winner: %v", game, g.tricksWon)
		}
		if g.scores[0]+g.scores[1] == 0 {
			t.Fatalf("hand %d awarded no points", game)
		}
	}
}

// TestRenderNoPanic drives the UI through each phase against a headless renderer
// to make sure the drawing and hit-testing paths never panic.
func TestRenderNoPanic(t *testing.T) {
	term := renderer.NewHeadless(80, 24)
	render := func() {
		ctx := miniui.New()
		renderer.Connect(ctx, term)
		st := term.Style()
		ctx.Style = &st
		ctx.Begin()
		ui(ctx, term)
		ctx.End()
	}

	g := &Game{}
	game = g

	g.hakem = seatYou
	g.newHand() // you are Hâkem: trump-choice buttons + first five
	g.nextActAt = time.Time{}
	render()

	g.setTrump(spades) // your turn: interactive hand with legal highlighting
	render()

	g.hakem = seatRight
	g.newHand() // an AI is Hâkem: "waiting" state
	g.nextActAt = time.Time{}
	render()

	g.tricksWon = [2]int{7, 3}
	g.endHand() // hand-over banner + "Next Hand" button
	render()

	g.scores = [2]int{6, 0}
	g.tricksWon = [2]int{7, 0}
	g.endHand() // game-over banner + "New Game" button
	render()
}
