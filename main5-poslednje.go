// main.go — server za igru preferans
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ==== Strukture ====
type Player struct {
	conn         *websocket.Conn
	room         string
	cards        []string
	bidValue     int
	bidDeclared  bool
	passed       bool
	id           int
	name         string
	prihvatio    bool   // da li je prihvatio igru
	refe         int    // broj refea
	declaredGame string // "igra", "betl", "sans"
	kontrirao    bool   // da li je dao kontru
}

type Room struct {
	players            []*Player
	talon              []string
	bids               int
	startIndex         int
	currentBidIndex    int
	highestBid         int
	highestBidder      *Player
	auctionDone        bool
	passCount          int
	dealCount          int
	kontraStatus       int // 0: nema, 1: kontra, 2: rekontra, 3: subkontra
	kontraBy           int // ID poslednjeg koji je rekao kontru
	kontraActive       bool
	prihvatili         int // broj igrača koji prate
	cekamoPracenje     bool
	maxRefe            int
	adut               string
	potvrdaOdigravanja bool // čeka se potvrda deklaranta
	cekamoKontru       int  // broj odgovora na prompt za kontru
	kontraPlayers      []int // ID-evi igrača koji su dali kontru/rekontru/subkontru

}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	rooms = make(map[string]*Room)
	mu   sync.Mutex
)

var deck = []string{
	"7♠", "8♠", "9♠", "10♠", "J♠", "Q♠", "K♠", "A♠",
	"7♦", "8♦", "9♦", "10♦", "J♦", "Q♦", "K♦", "A♦",
	"7♥", "8♥", "9♥", "10♥", "J♥", "Q♥", "K♥", "A♥",
	"7♣", "8♣", "9♣", "10♣", "J♣", "Q♣", "K♣", "A♣",
}

var rankOrder = map[string]int{
	"7": 1, "8": 2, "9": 3, "10": 4, "J": 5, "Q": 6, "K": 7, "A": 8,
}

var suitOrder = map[rune]int{
	'♠': 1, '♦': 2, '♥': 3, '♣': 4,
}

func sortCards(cards []string) {
	sort.Slice(cards, func(i, j int) bool {
		r1, s1 := parseCard(cards[i])
		r2, s2 := parseCard(cards[j])
		if suitOrder[s1] != suitOrder[s2] {
			return suitOrder[s1] < suitOrder[s2]
		}
		return rankOrder[r1] < rankOrder[r2]
	})
}

func parseCard(card string) (rank string, suit rune) {
	runes := []rune(card)
	if len(runes) == 3 {
		return string(runes[0:2]), runes[2]
	}
	return string(runes[0]), runes[1]
}

func startGame(r *Room) {
	if r.talonOtkriven {
		r.broadcast(map[string]any{
			"type":  "talon_info",
			"talon": r.talon,
		})
	}
	r.startIndex = (r.startIndex + 1) % 3

	r.broadcast(map[string]any{
		"type":    "start_game",
		"message": fmt.Sprintf("Igrač %d počinje igru sa adutom %s", r.highestBidder.id, r.adut),
	})

	for _, p := range r.players {
		sortCards(p.cards)
		p.conn.WriteJSON(map[string]any{
			"type":  "your_cards",
			"cards": p.cards,
		})
	}

	r.broadcast(map[string]any{
		"type":    "turn",
		"message": fmt.Sprintf("Igrač %d je na potezu. Baci kartu.", r.highestBidder.id),
		"player":  r.highestBidder.id,
	})
}
func getKontraMultiplier(r *Room) int {
	if !r.kontraActive {
		return 1
	}
	switch r.kontraStatus {
	case 1:
		return 2
	case 2:
		return 4
	case 3:
		return 8
	default:
		return 1
	}
}
func (r *Room) broadcast(msg map[string]any) {
	for _, p := range r.players {
		p.conn.WriteJSON(msg)
	}
}

func handleMessage(p *Player, msg []byte) {
	if p == nil || p.room == "" {
		return
	}
	var m map[string]interface{}
	if err := json.Unmarshal(msg, &m); err != nil {
		log.Println("Invalid message:", err)
		return
	}
	r := rooms[p.room]
	switch m["type"] {
	case "pass":
		if p.passed {
			return
		}
		p.passed = true
		if r != nil {
			r.passCount++
		}
		broadcast(p.room, map[string]any{
			"type":    "info",
			"message": fmt.Sprintf("Igrač %d kaže pas", p.id),
		})
		if r.passCount == 3 {
			broadcast(p.room, map[string]any{
				"type":    "info",
				"message": "Svi igrači su rekli pas. Nova podela.",
			})
			startGame(r)
			return
		}
		if r.passCount == 2 {
			// jedan igrač je ostao — završena licitacija
			r.auctionDone = true
			r.potvrdaOdigravanja = true
			r.highestBidder.conn.WriteJSON(map[string]any{
				"type":    "potvrdi_igru",
				"message": "Potvrdi šta igraš ili najavi veću igru.",
			})
			broadcast(r.players[0].room, map[string]any{
				"type":    "info",
				"message": "Čeka se potvrda deklaranta.",
			})
			return
		}
		next := (r.currentBidIndex + 1) % 3
		for r.players[next].passed {
			next = (next + 1) % 3
		}
		r.currentBidIndex = next
		r.players[next].conn.WriteJSON(map[string]any{
			"type":  "your_turn",
			"index": next,
		})

	case "potvrdi_igru":
		if p != r.highestBidder || !r.potvrdaOdigravanja {
			return
		}
		adutStr, ok := m["value"].(string)
		if !ok {
			return
		}
		r.adut = adutStr
		r.potvrdaOdigravanja = false
		r.broadcast(map[string]any{
			"type":    "info",
			"message": fmt.Sprintf("Igrač %d potvrđuje igru: %s", p.id, adutStr),
		})

		if r.highestBid <= 5 {
			// Igra se iz talona – svi vide talon, deklarant bira štil
			r.broadcast(map[string]any{
				"type":    "talon_info",
				"message": "Otkriven je talon.",
				"talon":   r.talon,
			})

			r.highestBidder.conn.WriteJSON(map[string]any{
				"type":    "biraj_stil",
				"message": "Izaberi dve karte za štil.",
				"cards":   r.talon,
			})
			return
		}

		// inače odmah pitaj za kontru
		r.cekamoKontru = 0
		for _, pl := range r.players {
			if pl != r.highestBidder {
				pl.conn.WriteJSON(map[string]any{
					"type":    "kontra_prompt",
					"message": "Da li daješ kontru?",
				})
				r.cekamoKontru++
			}
		}
	case "kontra_odgovor":
		ox, ok := m["kontra"].(bool)
		if !ok || r == nil {
			return
		}
		if ox {
			r.kontraStatus++
			r.kontraBy = p.id
			r.kontraActive = true
			r.kontraPlayers = append(r.kontraPlayers, p.id)
			if r.kontraStatus > 3 {
				r.kontraStatus = 3
			}
			r.broadcast(map[string]any{
				"type":    "kontra_info",
				"message": fmt.Sprintf("Igrač %d daje %s.", p.id, []string{"kontru", "rekontru", "subkontru"}[r.kontraStatus-1]),
			})
		}
		r.cekamoKontru--
		if r.cekamoKontru == 0 {
			startGame(r)
		}
    case "baci_kartu":
		card, ok := m["card"].(string)
		if !ok {
			return
		}
		r.broadcast(map[string]any{
			"type":    "karta_bacena",
			"message": fmt.Sprintf("Igrač %d baca %s", p.id, card),
			"card":    card,
			"player":  p.id,
		})
	case "stil_odabran":
		selected, ok := m["discard"].([]interface{})
		if !ok || len(selected) != 2 {
			p.conn.WriteJSON(map[string]any{
				"type":    "error",
				"message": "Moraš odbaciti tačno dve karte.",
			})
			return
		}
		// konvertuj u []string
		var stil []string
		for _, c := range selected {
			if s, ok := c.(string); ok {
				stil = append(stil, s)
			}
		}
		// ukloni iz talona i dodaj ostale karte deklarantu
		final := []string{}
		for _, c := range r.talon {
			if c != stil[0] && c != stil[1] {
				final = append(final, c)
			}
		}
		p.cards = append(p.cards, final...)
		sortCards(p.cards)

		p.conn.WriteJSON(map[string]any{
			"type":    "info",
			"message": "Odabrao si štil. Počinje igra.",
			"cards":   p.cards,
		})

		r.cekamoKontru = 0
		for _, pl := range r.players {
			if pl != p {
				pl.conn.WriteJSON(map[string]any{
					"type":    "kontra_prompt",
					"message": "Da li daješ kontru?",
				})
				r.cekamoKontru++
			}
		}
		return
	case "odbaci_karte":
		lista, _ := m["karte"].([]interface{})
		odabrane := make(map[string]bool)
		for _, k := range lista {
			if ks, ok := k.(string); ok {
				odabrane[ks] = true
			}
		}
		novaRuka := []string{}
		for _, c := range p.cards {
			if !odabrane[c] {
				novaRuka = append(novaRuka, c)
			}
		}
		if len(novaRuka) != 10 {
			p.conn.WriteJSON(map[string]any{
				"type":    "error",
				"message": "Moraš odbaciti tačno 2 karte!",
			})
			return
		}
		p.cards = novaRuka
		r.cekamoKontru = 2
		for _, pp := range r.players {
			if pp != p {
				pp.conn.WriteJSON(map[string]any{
					"type":    "kontra_prompt",
					"message": fmt.Sprintf("Da li igrač %d može da igra ili kontriraš?", p.id),
				})
			}
		}
		p.conn.WriteJSON(map[string]any{
			"type":    "info",
			"message": "Čekamo da protivnici odluče o kontri...",
		})
    case "igra":
		// Samo igrači koji još nisu licitirali mogu da kažu "igra"
		if p.bidDeclared || p.passed {
			return
		}
		val, _ := m["value"].(string)
		if val != "igra" && val != "betl" && val != "sans" {
			return
		}
		p.declaredGame = val
		broadcast(p.room, map[string]any{
			"type":    "info",
			"message": fmt.Sprintf("Igrač %d deklariše: %s", p.id, val),
		})
		// Uporedi jačinu igre i odredi pobednika licitacije
		if r.highestBidder == nil || jačaDeklaracija(val, r.highestBidder.declaredGame) {
			r.highestBidder = p
			r.highestBid = map[string]int{"igra": 8, "betl": 9, "sans": 10}[val]
		}
		// Idi na sledećeg igrača
		next := (r.currentBidIndex + 1) % 3
		r.currentBidIndex = next
		r.players[next].conn.WriteJSON(map[string]any{
			"type":  "your_turn",
			"index": next,
		})
	}
	case "kontra_odgovor":
		ox, ok := m["kontra"].(bool)
		if !ok || r == nil {
			return
		}
		if ox {
			r.kontraStatus++
			r.kontraBy = p.id
			r.kontraActive = true
			r.kontraPlayers = append(r.kontraPlayers, p.id)
			if r.kontraStatus > 3 {
				r.kontraStatus = 3
			}
			r.broadcast(map[string]any{
				"type":    "kontra_info",
				"message": fmt.Sprintf("Igrač %d daje %s.", p.id, []string{"kontru", "rekontru", "subkontru"}[r.kontraStatus-1]),
			})
		}
		r.cekamoKontru--
		if r.cekamoKontru == 0 {
			startGame(r)
		}
	}
}

func jačaDeklaracija(a, b string) bool {
	redosled := map[string]int{"igra": 1, "betl": 2, "sans": 3}
	return redosled[a] > redosled[b]
}
