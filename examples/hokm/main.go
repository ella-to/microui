// Command hokm is a playable, terminal Hokm card game built on miniui — a single
// human plays against three AIs (the one across the table is your partner, the
// two on the sides are opponents).
//
//	go run ella.to/microui/examples/hokm
//
// Hokm is Iran's most popular trick-taking game. Each hand one player is the
// Hâkem ("ruler"): they choose the trump suit ("hokm") from their first five
// cards and lead the first trick. Players must follow the led suit; the highest
// trump wins a trick, otherwise the highest card of the led suit. The first team
// to seven tricks wins the hand (a 7–0 sweep is worth more), and the first team
// to seven points wins the game.
//
// This example is a showcase of driving miniui as an immediate-mode game UI:
// it draws a full-screen canvas window and hit-tests its own colored cards with
// MouseOver + MousePressed instead of using the built-in controls, and it paces
// the AIs with timed delays so each trick is watchable. Click a highlighted card
// in your hand to play it; press Ctrl-C to quit.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"time"

	miniui "ella.to/microui"
	"ella.to/microui/pkg/renderer"
)

// ---- cards ------------------------------------------------------------------

// Suit is one of the four card suits. noTrump is a sentinel used while no trump
// has been chosen (e.g. when sorting a hand before the Hâkem decides).
type Suit int

const (
	spades Suit = iota
	hearts
	diamonds
	clubs
	noTrump Suit = -1
)

// Card is a single playing card. Rank runs 2..14, with 11=J, 12=Q, 13=K, 14=A.
type Card struct {
	Suit Suit
	Rank int
}

func suitGlyph(s Suit) string { return [...]string{"♠", "♥", "♦", "♣"}[s] }
func suitFull(s Suit) string  { return [...]string{"Spades", "Hearts", "Diamonds", "Clubs"}[s] }

// rankStr renders a rank as it appears on a card.
func rankStr(r int) string {
	switch r {
	case 14:
		return "A"
	case 13:
		return "K"
	case 12:
		return "Q"
	case 11:
		return "J"
	default:
		return strconv.Itoa(r)
	}
}

func cardLabel(c Card) string { return rankStr(c.Rank) + suitGlyph(c.Suit) }

// freshDeck returns a sorted 52-card deck.
func freshDeck() []Card {
	d := make([]Card, 0, 52)
	for s := spades; s <= clubs; s++ {
		for r := 2; r <= 14; r++ {
			d = append(d, Card{Suit: s, Rank: r})
		}
	}
	return d
}

// beats reports whether card a would beat card b in a trick where trump is the
// trump suit and led is the suit that was led. A trump beats any non-trump; if
// both are trump (or both follow the led suit) the higher rank wins; a card that
// neither trumps nor follows the led suit can never beat anything.
func beats(a, b Card, trump, led Suit) bool {
	aTrump, bTrump := a.Suit == trump, b.Suit == trump
	if aTrump != bTrump {
		return aTrump
	}
	if aTrump { // both trump
		return a.Rank > b.Rank
	}
	aLed, bLed := a.Suit == led, b.Suit == led
	if aLed != bLed {
		return aLed
	}
	if aLed { // both follow the led suit
		return a.Rank > b.Rank
	}
	return false
}

// suitOrder gives a stable display order for a hand, putting the trump suit
// first so the player can see their trump at a glance.
func suitOrder(s, trump Suit) int {
	if s == trump {
		return -1
	}
	return int(s)
}

// sortHand orders a hand by suit (trump first) then by rank, high to low.
func sortHand(cards []Card, trump Suit) {
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := cards[i], cards[j]
		if oa, ob := suitOrder(a.Suit, trump), suitOrder(b.Suit, trump); oa != ob {
			return oa < ob
		}
		return a.Rank > b.Rank
	})
}

// ---- seats & teams ----------------------------------------------------------

// Seats are arranged around the table; play proceeds anticlockwise, so the
// turn order seatYou -> seatRight -> seatPartner -> seatLeft alternates teams.
const (
	seatYou     = 0 // bottom (the human)
	seatRight   = 1 // right side (opponent)
	seatPartner = 2 // across the table (your AI partner)
	seatLeft    = 3 // left side (opponent)
)

var seatName = [4]string{"You", "Right", "Partner", "Left"}

// teamOf returns 0 for your team (you + partner) or 1 for the opponents.
func teamOf(seat int) int { return seat % 2 }

var teamName = [2]string{"Your team", "Opponents"}

// next returns the seat that plays after seat (anticlockwise).
func next(seat int) int { return (seat + 1) % 4 }

// ---- game state -------------------------------------------------------------

// Phase is the current stage of a hand.
type Phase int

const (
	phaseChooseTrump Phase = iota // Hâkem is picking trump
	phasePlaying                  // a trick is in progress
	phaseTrickDone                // four cards are down; lingering before clearing
	phaseHandDone                 // a team reached 7 tricks; awaiting "Next Hand"
	phaseGameOver                 // a team reached 7 points; awaiting "New Game"
)

// Pacing for the AIs so play is watchable rather than instant.
const (
	aiThink     = 650 * time.Millisecond
	trickLinger = 1300 * time.Millisecond
)

// Game holds all state for a Hokm match, persistent across frames.
type Game struct {
	hands     [4][]Card // each seat's remaining cards
	trick     [4]Card   // card played by each seat this trick
	played    [4]bool   // whether each seat has played this trick
	firstFive []Card    // the Hâkem's first five cards, used to choose trump

	trump       Suit
	trumpChosen bool
	hakem       int

	leader     int    // who leads the current trick
	turn       int    // whose turn it is
	lastWinner int    // winner of the trick currently on the table
	tricksWon  [2]int // tricks taken this hand, per team
	scores     [2]int // game points, per team

	phase     Phase
	handNo    int
	message   string    // one-line status under the scoreboard
	banner    string    // big centered text for hand/game over
	nextActAt time.Time // earliest time the next timed action may run
}

// deal shuffles a deck and gives 13 cards to each seat, recording the Hâkem's
// first five (for trump selection) before the human's hand is sorted.
func (g *Game) deal() {
	d := freshDeck()
	rand.Shuffle(len(d), func(i, j int) { d[i], d[j] = d[j], d[i] })
	for s := range 4 {
		g.hands[s] = append([]Card(nil), d[s*13:(s+1)*13]...)
	}
	g.firstFive = append([]Card(nil), g.hands[g.hakem][:5]...)
	sortHand(g.firstFive, noTrump)
	sortHand(g.hands[seatYou], noTrump)
}

// newHand starts a fresh hand, keeping scores and the current Hâkem.
func (g *Game) newHand() {
	g.handNo++
	g.tricksWon = [2]int{}
	g.played = [4]bool{}
	g.trick = [4]Card{}
	g.trumpChosen = false
	g.banner = ""
	g.deal()
	g.leader, g.turn = g.hakem, g.hakem
	g.phase = phaseChooseTrump
	if g.hakem == seatYou {
		g.message = "You are Hâkem — choose trump from your first five cards."
	} else {
		g.message = fmt.Sprintf("%s is Hâkem and is choosing trump…", seatLong(g.hakem))
		g.nextActAt = time.Now().Add(aiThink)
	}
}

// newGame resets scores and randomly assigns the first Hâkem.
func (g *Game) newGame() {
	g.scores = [2]int{}
	g.handNo = 0
	g.hakem = rand.Intn(4)
	g.newHand()
}

// setTrump records the chosen trump and begins play.
func (g *Game) setTrump(s Suit) {
	g.trump = s
	g.trumpChosen = true
	sortHand(g.hands[seatYou], g.trump)
	g.phase = phasePlaying
	g.leader, g.turn = g.hakem, g.hakem
	g.message = fmt.Sprintf("Trump is %s %s. %s leads.", suitGlyph(s), suitFull(s), seatLong(g.hakem))
	if g.turn != seatYou {
		g.nextActAt = time.Now().Add(aiThink)
	}
}

// noneePlayed reports whether the current trick is empty (so the seat to act is
// leading and may play anything).
func (g *Game) nonePlayed() bool {
	for _, p := range g.played {
		if p {
			return false
		}
	}
	return true
}

func (g *Game) allPlayed() bool {
	for _, p := range g.played {
		if !p {
			return false
		}
	}
	return true
}

// legal returns the indices of the cards seat may legally play: any card when
// leading, otherwise the led suit if held, otherwise any card.
func (g *Game) legal(seat int) []int {
	hand := g.hands[seat]
	all := func() []int {
		idx := make([]int, len(hand))
		for i := range idx {
			idx[i] = i
		}
		return idx
	}
	if g.nonePlayed() {
		return all()
	}
	led := g.trick[g.leader].Suit
	var follow []int
	for i, c := range hand {
		if c.Suit == led {
			follow = append(follow, i)
		}
	}
	if len(follow) > 0 {
		return follow
	}
	return all()
}

// currentBest returns the seat currently winning the trick, scanning in play
// order from the leader. ok is false when no card has been played yet.
func (g *Game) currentBest() (seat int, ok bool) {
	led := g.trick[g.leader].Suit
	best := -1
	s := g.leader
	for range 4 {
		if g.played[s] && (best == -1 || beats(g.trick[s], g.trick[best], g.trump, led)) {
			best = s
		}
		s = next(s)
	}
	if best == -1 {
		return -1, false
	}
	return best, true
}

// play moves card idx from seat's hand onto the table and advances the game:
// either completing the trick (entering phaseTrickDone) or passing the turn.
func (g *Game) play(seat, idx int) {
	g.trick[seat] = g.hands[seat][idx]
	g.played[seat] = true
	g.hands[seat] = append(g.hands[seat][:idx], g.hands[seat][idx+1:]...)

	if g.allPlayed() {
		w, _ := g.currentBest()
		g.lastWinner = w
		g.message = fmt.Sprintf("%s wins the trick.", seatLong(w))
		g.phase = phaseTrickDone
		g.nextActAt = time.Now().Add(trickLinger)
		return
	}
	g.turn = next(seat)
	if g.turn != seatYou {
		g.nextActAt = time.Now().Add(aiThink)
	}
}

// resolveTrick awards the completed trick to its winner and either ends the hand
// (a team reached 7 tricks) or starts the next trick with the winner leading.
func (g *Game) resolveTrick() {
	w := g.lastWinner
	g.tricksWon[teamOf(w)]++
	g.played = [4]bool{}
	g.trick = [4]Card{}

	if g.tricksWon[0] >= 7 || g.tricksWon[1] >= 7 {
		g.endHand()
		return
	}
	g.leader, g.turn = w, w
	g.message = fmt.Sprintf("%s leads.", seatLong(w))
	if g.turn != seatYou {
		g.nextActAt = time.Now().Add(aiThink)
	}
	g.phase = phasePlaying
}

// endHand scores the finished hand, rotates the Hâkem per the rules, and checks
// for the end of the game.
func (g *Game) endHand() {
	winTeam := 0
	if g.tricksWon[1] >= 7 {
		winTeam = 1
	}
	loser := 1 - winTeam
	pts := 1
	sweep := g.tricksWon[loser] == 0
	switch {
	case sweep && winTeam == teamOf(g.hakem):
		pts = 2 // Hâkem's team swept ("kot")
	case sweep:
		pts = 3 // opponents swept the Hâkem ("kot-e hâkem")
	}
	g.scores[winTeam] += pts

	// If the Hâkem's team lost, the turn passes to the player on the Hâkem's
	// right, who becomes the new Hâkem; otherwise the Hâkem keeps the rank.
	if winTeam != teamOf(g.hakem) {
		g.hakem = next(g.hakem)
	}

	result := fmt.Sprintf("%s won the hand %d–%d (+%d).",
		teamName[winTeam], g.tricksWon[winTeam], g.tricksWon[loser], pts)
	if sweep {
		result = fmt.Sprintf("KOT! %s swept the hand 7–0 (+%d).", teamName[winTeam], pts)
	}

	if g.scores[0] >= 7 || g.scores[1] >= 7 {
		champ := 0
		if g.scores[1] >= 7 {
			champ = 1
		}
		g.phase = phaseGameOver
		g.banner = fmt.Sprintf("%s   —   %s wins the game %d–%d!",
			result, teamName[champ], g.scores[champ], g.scores[1-champ])
		g.message = "Game over."
		return
	}
	g.phase = phaseHandDone
	g.banner = fmt.Sprintf("%s   Next Hâkem: %s.", result, seatLong(g.hakem))
	g.message = fmt.Sprintf("Score — %s %d, %s %d.", teamName[0], g.scores[0], teamName[1], g.scores[1])
}

// ---- AI ---------------------------------------------------------------------

// aiPickTrump chooses trump as the suit the Hâkem is longest in (ties broken by
// total rank), considering only the first five cards as the rules require.
func aiPickTrump(five []Card) Suit {
	var count, strength [4]int
	for _, c := range five {
		count[c.Suit]++
		strength[c.Suit] += c.Rank
	}
	best := spades
	for s := hearts; s <= clubs; s++ {
		if count[s] > count[best] || (count[s] == count[best] && strength[s] > strength[best]) {
			best = s
		}
	}
	return best
}

// aiPlay returns the hand index the AI at seat should play. It cooperates with a
// winning partner by ducking cheaply, and tries to win as cheaply as possible
// (preserving trumps) when an opponent is ahead.
func (g *Game) aiPlay(seat int) int {
	hand := g.hands[seat]
	legal := g.legal(seat)

	if g.nonePlayed() {
		return g.aiLead(seat, legal)
	}

	led := g.trick[g.leader].Suit
	bestSeat, _ := g.currentBest()
	bestCard := g.trick[bestSeat]

	if teamOf(bestSeat) == teamOf(seat) {
		// Partner is winning — throw away the cheapest junk.
		return cheapestDiscard(hand, legal, g.trump)
	}

	// Opponent is winning — win as cheaply as possible if we can.
	win := -1
	for _, i := range legal {
		if beats(hand[i], bestCard, g.trump, led) {
			if win == -1 || cheaperWinner(hand[i], hand[win], g.trump) {
				win = i
			}
		}
	}
	if win >= 0 {
		return win
	}
	return cheapestDiscard(hand, legal, g.trump)
}

// aiLead leads the highest card of the longest non-trump suit, falling back to
// the highest trump if only trumps remain.
func (g *Game) aiLead(seat int, legal []int) int {
	hand := g.hands[seat]
	var bySuit [4][]int
	for _, i := range legal {
		bySuit[hand[i].Suit] = append(bySuit[hand[i].Suit], i)
	}
	bestSuit := -1
	best := -1
	for s := spades; s <= clubs; s++ {
		if Suit(s) == g.trump || len(bySuit[s]) == 0 {
			continue
		}
		hi := highest(hand, bySuit[s])
		switch {
		case bestSuit == -1,
			len(bySuit[s]) > len(bySuit[bestSuit]),
			len(bySuit[s]) == len(bySuit[bestSuit]) && hand[hi].Rank > hand[best].Rank:
			bestSuit, best = int(s), hi
		}
	}
	if best >= 0 {
		return best
	}
	return highest(hand, bySuit[g.trump]) // only trumps left
}

// highest returns the index (from idxs) of the highest-ranked card in hand.
func highest(hand []Card, idxs []int) int {
	best := idxs[0]
	for _, i := range idxs {
		if hand[i].Rank > hand[best].Rank {
			best = i
		}
	}
	return best
}

// cheaperWinner reports whether a is a better card to win with than b: prefer a
// non-trump win, then the lower rank, so trumps and high cards are conserved.
func cheaperWinner(a, b Card, trump Suit) bool {
	if at, bt := a.Suit == trump, b.Suit == trump; at != bt {
		return !at
	}
	return a.Rank < b.Rank
}

// cheapestDiscard returns the index of the best card to throw away: the lowest
// non-trump, or the lowest trump if nothing else is legal.
func cheapestDiscard(hand []Card, legal []int, trump Suit) int {
	best := legal[0]
	for _, i := range legal {
		if cheaperWinner(hand[i], hand[best], trump) {
			best = i
		}
	}
	return best
}

// ---- palette ----------------------------------------------------------------

var (
	feltOuter  = miniui.RGBA(11, 54, 33, 255)  // backdrop
	feltInner  = miniui.RGBA(18, 84, 50, 255)  // table surface
	feltLine   = miniui.RGBA(40, 120, 78, 255) // table edge
	cardFace   = miniui.RGBA(236, 233, 220, 255)
	cardHot    = miniui.RGBA(255, 248, 206, 255) // hovered, playable
	cardDim    = miniui.RGBA(110, 120, 112, 255) // illegal this turn
	cardBack   = miniui.RGBA(44, 70, 150, 255)
	backPip    = miniui.RGBA(120, 150, 230, 255)
	redSuit    = miniui.RGBA(200, 50, 50, 255)
	blackSuit  = miniui.RGBA(28, 28, 32, 255)
	winnerGlow = miniui.RGBA(232, 196, 78, 255) // trick-winner highlight
	textBright = miniui.RGBA(238, 238, 230, 255)
	textDim    = miniui.RGBA(170, 188, 176, 255)
	gold       = miniui.RGBA(240, 205, 90, 255)
	btnFace    = miniui.RGBA(58, 92, 162, 255)
	btnHot     = miniui.RGBA(86, 126, 206, 255)
)

func suitColor(s Suit) miniui.Color {
	if s == hearts || s == diamonds {
		return redSuit
	}
	return blackSuit
}

// seatLong returns a descriptive seat name including its role.
func seatLong(seat int) string {
	switch seat {
	case seatYou:
		return "You"
	case seatPartner:
		return "Partner"
	default:
		return seatName[seat] + " opp."
	}
}

// ---- drawing helpers --------------------------------------------------------

func runeLen(s string) int { return len([]rune(s)) }

// drawTextc draws s with its top-left at (x,y) in color col.
func drawTextc(ctx *miniui.Context, x, y int, s string, col miniui.Color) {
	ctx.DrawText(nil, s, miniui.Vec2{X: x, Y: y}, col)
}

// drawCenteredIn draws s centered within rect r in color col.
func drawCenteredIn(ctx *miniui.Context, r miniui.Rect, s string, col miniui.Color) {
	x := r.X + (r.W-runeLen(s))/2
	y := r.Y + r.H/2
	drawTextc(ctx, x, y, s, col)
}

// drawCard paints a face-up card filling rect r with the given face color.
func drawCard(ctx *miniui.Context, r miniui.Rect, c Card, face miniui.Color) {
	ctx.DrawRect(r, face)
	drawCenteredIn(ctx, r, cardLabel(c), suitColor(c.Suit))
}

// drawPlayableCard draws a face-up card with a gold base on its own bottom row,
// marking it as a legal play. The base stays inside the card's footprint, so it
// never paints into the rows beneath the hand.
func drawPlayableCard(ctx *miniui.Context, r miniui.Rect, c Card, face miniui.Color) {
	ctx.DrawRect(r, face)
	ctx.DrawRect(miniui.Rect{X: r.X, Y: r.Y + r.H - 1, W: r.W, H: 1}, gold)
	drawCenteredIn(ctx, r, cardLabel(c), suitColor(c.Suit))
}

// drawCardBack paints a face-down card.
func drawCardBack(ctx *miniui.Context, r miniui.Rect) {
	ctx.DrawRect(r, cardBack)
	drawCenteredIn(ctx, r, "▚▚", backPip)
}

// button draws a clickable button and reports whether it was clicked this frame.
func button(ctx *miniui.Context, r miniui.Rect, label string, face, hot miniui.Color) bool {
	over := ctx.MouseOver(r)
	col := face
	if over {
		col = hot
	}
	ctx.DrawRect(r, col)
	drawCenteredIn(ctx, r, label, textBright)
	return over && ctx.MousePressed()&miniui.MouseLeft != 0
}

// ---- the UI -----------------------------------------------------------------

var game = &Game{}

// trickSlots returns each seat's card position around the center of the table.
func trickSlots(cx, cy int) [4]miniui.Rect {
	var s [4]miniui.Rect
	s[seatYou] = miniui.Rect{X: cx - 2, Y: cy + 2, W: 4, H: 3}
	s[seatRight] = miniui.Rect{X: cx + 9, Y: cy - 1, W: 4, H: 3}
	s[seatPartner] = miniui.Rect{X: cx - 2, Y: cy - 4, W: 4, H: 3}
	s[seatLeft] = miniui.Rect{X: cx - 13, Y: cy - 1, W: 4, H: 3}
	return s
}

func ui(ctx *miniui.Context, term *renderer.Terminal) {
	term.BG = feltOuter
	w, h := term.Size()

	const canvas = miniui.OptNoTitle | miniui.OptNoResize | miniui.OptNoScroll | miniui.OptNoFrame
	if ctx.BeginWindowEx("hokm", miniui.Rect{X: 0, Y: 0, W: 10000, H: 10000}, canvas) == 0 {
		return
	}
	defer ctx.EndWindow()

	if w < 56 || h < 24 {
		drawTextc(ctx, 1, 1, "Please resize the terminal to at least 56x24.", textBright)
		return
	}

	g := game

	// --- timed transitions (AI thinking, trick lingering) --------------------
	now := time.Now()
	switch {
	case g.phase == phaseChooseTrump && g.hakem != seatYou && now.After(g.nextActAt):
		g.setTrump(aiPickTrump(g.firstFive))
	case g.phase == phasePlaying && g.turn != seatYou && now.After(g.nextActAt):
		g.play(g.turn, g.aiPlay(g.turn))
	case g.phase == phaseTrickDone && now.After(g.nextActAt):
		g.resolveTrick()
	}

	drawTable(ctx, g, w, h)
	clicked := drawHand(ctx, g, w, h)
	drawStatus(ctx, g, w, h)
	drawOverlays(ctx, g, w, h)

	// Human plays a card by clicking a legal one in their hand.
	if g.phase == phasePlaying && g.turn == seatYou && clicked >= 0 {
		g.play(seatYou, clicked)
	}
}

// drawTable paints the felt, each seat's name/turn indicator and remaining-card
// count, and any cards currently on the table.
func drawTable(ctx *miniui.Context, g *Game, w, h int) {
	// The felt stops short of the bottom rows, leaving room below it for the
	// "You" label, the hand (with a row above it for the hover lift), and the
	// message and hint lines on the last two rows.
	felt := miniui.Rect{X: 3, Y: 2, W: w - 6, H: h - 9}
	ctx.DrawRect(felt, feltInner)
	ctx.DrawBox(felt, feltLine)

	cx, cy := w/2, h/2
	slots := trickSlots(cx, cy)

	// Seat banners around the edge of the table.
	drawSeatBanner(ctx, g, seatPartner, cx-7, 3)
	drawSeatBanner(ctx, g, seatLeft, 5, cy-3)
	drawSeatBanner(ctx, g, seatRight, w-19, cy-3)

	// Played cards (highlight the winner while the trick lingers).
	for s := range 4 {
		if !g.played[s] {
			continue
		}
		face := cardFace
		if g.phase == phaseTrickDone && s == g.lastWinner {
			face = winnerGlow
		}
		drawCard(ctx, slots[s], g.trick[s], face)
	}
}

// drawSeatBanner draws one AI seat's name (gold when it is their turn) plus a
// face-down stack and remaining-card count.
func drawSeatBanner(ctx *miniui.Context, g *Game, seat, x, y int) {
	name := seatLong(seat)
	if seat == g.hakem {
		name += " ★" // Hâkem marker
	}
	col := textDim
	if g.turn == seat && (g.phase == phasePlaying || g.phase == phaseChooseTrump) {
		col = gold
		name = "▸ " + name
	}
	drawTextc(ctx, x, y, name, col)
	drawCardBack(ctx, miniui.Rect{X: x, Y: y + 1, W: 4, H: 2})
	drawTextc(ctx, x+5, y+2, "×"+strconv.Itoa(len(g.hands[seat])), textDim)
}

// drawHand draws the human's cards along the bottom and returns the index of a
// legal card clicked this frame, or -1. While choosing trump it shows only the
// first five cards (non-interactive), per the rules.
func drawHand(ctx *miniui.Context, g *Game, w, h int) int {
	cards := g.hands[seatYou]
	if g.phase == phaseChooseTrump && g.hakem == seatYou && !g.trumpChosen {
		cards = g.firstFive
	}
	interactive := g.phase == phasePlaying && g.turn == seatYou

	legalSet := map[int]bool{}
	if interactive {
		for _, i := range g.legal(seatYou) {
			legalSet[i] = true
		}
	}

	// "You" banner.
	you := "You"
	if g.hakem == seatYou {
		you += " ★ (Hâkem)"
	}
	if interactive {
		you = "▸ Your turn — click a card"
	}
	drawTextc(ctx, (w-runeLen(you))/2, h-7, you, ternColor(interactive, gold, textDim))

	n := len(cards)
	cw, gap := 4, 1
	total := n*cw + (n-1)*gap
	startX := max((w-total)/2, 1)
	y := h - 5
	clicked := -1
	for i, c := range cards {
		r := miniui.Rect{X: startX + i*(cw+gap), Y: y, W: cw, H: 3}
		switch {
		case !interactive:
			drawCard(ctx, r, c, cardFace)
		case !legalSet[i]:
			drawCard(ctx, r, c, cardDim) // can't follow with this card right now
		case ctx.MouseOver(r):
			// Hovered playable card lifts up a row for a tactile feel.
			lifted := miniui.Rect{X: r.X, Y: r.Y - 1, W: cw, H: 3}
			drawPlayableCard(ctx, lifted, c, cardHot)
			if ctx.MousePressed()&miniui.MouseLeft != 0 {
				clicked = i
			}
		default:
			drawPlayableCard(ctx, r, c, cardFace) // playable
		}
	}
	return clicked
}

// drawStatus draws the title bar: scoreboard, trump, Hâkem and the message line.
func drawStatus(ctx *miniui.Context, g *Game, w, h int) {
	title := "♠ HOKM ♥"
	drawTextc(ctx, 2, 0, title, gold)

	score := fmt.Sprintf("%s %d   |   %s %d   (first to 7)",
		teamName[0], g.scores[0], teamName[1], g.scores[1])
	drawTextc(ctx, w-runeLen(score)-2, 0, score, textBright)

	trump := "Trump: —"
	tcol := textDim
	if g.trumpChosen {
		trump = "Trump: " + suitGlyph(g.trump) + " " + suitFull(g.trump)
		tcol = suitColor(g.trump)
	}
	drawTextc(ctx, 2, 1, trump, tcol)

	info := fmt.Sprintf("Hâkem: %s   Tricks — %s %d, %s %d",
		seatLong(g.hakem), teamName[0], g.tricksWon[0], teamName[1], g.tricksWon[1])
	drawTextc(ctx, w-runeLen(info)-2, 1, info, textBright)

	if g.message != "" {
		drawTextc(ctx, (w-runeLen(g.message))/2, h-2, g.message, textBright)
	}
	hint := "Click a highlighted card to play   •   Ctrl-C to quit"
	drawTextc(ctx, (w-runeLen(hint))/2, h-1, hint, textDim)
}

// drawOverlays draws the trump-choice buttons, and the hand/game-over banner
// with its action button, depending on phase.
func drawOverlays(ctx *miniui.Context, g *Game, w, h int) {
	cx, cy := w/2, h/2

	// Trump selection (only when you are Hâkem).
	if g.phase == phaseChooseTrump && g.hakem == seatYou && !g.trumpChosen {
		prompt := "Pick the trump suit (hokm):"
		drawTextc(ctx, (w-runeLen(prompt))/2, cy-2, prompt, textBright)
		bw, bgap := 11, 1
		total := 4*bw + 3*bgap
		x := (w - total) / 2
		for s := spades; s <= clubs; s++ {
			r := miniui.Rect{X: x, Y: cy, W: bw, H: 3}
			label := suitGlyph(s) + " " + suitFull(s)
			if button(ctx, r, label, btnFace, btnHot) {
				g.setTrump(s)
			}
			x += bw + bgap
		}
		return
	}

	// Hand- and game-over banners with a continue button.
	if g.phase == phaseHandDone || g.phase == phaseGameOver {
		// Dim the table behind the banner for focus.
		drawTextc(ctx, (w-runeLen(g.banner))/2, cy-2, g.banner, gold)

		label, action := "Next Hand", g.newHand
		if g.phase == phaseGameOver {
			label, action = "New Game", g.newGame
		}
		bw := runeLen(label) + 6
		r := miniui.Rect{X: cx - bw/2, Y: cy + 1, W: bw, H: 3}
		if button(ctx, r, label, btnFace, btnHot) {
			action()
		}
	}
}

// ternColor is a tiny ternary helper for colors.
func ternColor(cond bool, a, b miniui.Color) miniui.Color {
	if cond {
		return a
	}
	return b
}

func main() {
	term, err := renderer.NewTerminal()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hokm:", err)
		os.Exit(1)
	}
	game.newGame()
	err = renderer.Run(term, func(ctx *miniui.Context) { ui(ctx, term) })
	if err != nil {
		fmt.Fprintln(os.Stderr, "hokm:", err)
		os.Exit(1)
	}
}
