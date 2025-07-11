// main.go — server za igru preferans
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"

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
	potvrdaOdigravanja bool  // čeka se potvrda deklaranta
	cekamoKontru       int   // broj odgovora na prompt za kontru
	kontraPlayers      []int // ID-evi igrača koji su dali kontru/rekontru/subkontru
	talonOtkriven      bool
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	ooms = make(map[string]*Room)
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
func main() {
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/", serveHome)
	http.Handle("/cards/", http.StripPrefix("/cards/", http.FileServer(http.Dir("static/cards"))))
	fmt.Println("Server running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
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
	r := ooms[p.room]
	switch m["type"] {

	case "stil_odabran":
		stil, _ := m["stil"].(string)
		r.adut = stil
		r.broadcast(map[string]any{
			"type":    "adut_info",
			"message": fmt.Sprintf("Deklarant %d bira adut: %s", p.id, stil),
		})
		r.talonOtkriven = true
		r.broadcast(map[string]any{
			"type":  "talon_info",
			"talon": r.talon,
		})
		p.cards = append(p.cards, r.talon...)
		sortCards(p.cards)
		p.conn.WriteJSON(map[string]any{
			"type":  "discard_talon",
			"cards": p.cards,
		})

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
