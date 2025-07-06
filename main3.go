package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
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
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	rooms = make(map[string]*Room)
	mu    sync.Mutex
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
	rand.Seed(time.Now().UnixNano())
	shuffled := make([]string, len(deck))
	copy(shuffled, deck)
	rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})
	for i, p := range r.players {
		p.cards = shuffled[i*10 : (i+1)*10]
		sortCards(p.cards)
		p.bidDeclared = false
		p.passed = false
		p.bidValue = 0
		p.prihvatio = false
		p.refe = 0
		p.declaredGame = ""
		sendCards(p)
	}
	r.talon = shuffled[30:]
	sortCards(r.talon)
	r.startIndex = (r.startIndex + 1) % 3
	r.currentBidIndex = r.startIndex
	r.highestBid = 0
	r.highestBidder = nil
	r.passCount = 0
	r.auctionDone = false
	r.kontraStatus = 0
	r.kontraBy = -1
	r.kontraActive = false
	r.prihvatili = 0
	r.cekamoPracenje = false
	r.adut = ""
	r.potvrdaOdigravanja = false
	if r.maxRefe == 0 {
		r.maxRefe = 3
	}
	r.dealCount++
	broadcastMessage(r, fmt.Sprintf("Licitaciju počinje igrač %d", r.startIndex))
	r.players[r.currentBidIndex].conn.WriteJSON(map[string]any{
		"type":  "your_turn",
		"index": r.currentBidIndex,
	})
	// Počinje prva ruka — prvi baca deklarant
	firstToPlay := r.highestBidder.id
	r.broadcast(map[string]any{
		"type":    "turn",
		"message": fmt.Sprintf("Igrač %d je na potezu. Baci kartu.", firstToPlay),
		"player":  firstToPlay,
	})
}

// Ovo je pomoćna funkcija koja poredi jačinu deklaracije igre
func jačaDeklaracija(a, b string) bool {
	redosled := map[string]int{"igra": 1, "betl": 2, "sans": 3}
	return redosled[a] > redosled[b]
}
func assignToRoom(p *Player) string {
	mu.Lock()
	defer mu.Unlock()
	for id, room := range rooms {
		if len(room.players) < 3 {
			p.id = len(room.players)
			room.players = append(room.players, p)
			p.room = id
			if len(room.players) == 3 {
				go startGame(room)
			}
			return id
		}
	}
	newID := fmt.Sprintf("room%d", len(rooms)+1)
	rooms[newID] = &Room{players: []*Player{p}}
	p.room = newID
	p.id = 0
	return newID
}

func handleMessage(p *Player, msg []byte) {
	var m map[string]interface{}
	if err := json.Unmarshal(msg, &m); err != nil {
		log.Println("Invalid message:", err)
		return
	}
	r := rooms[p.room]
	switch m["type"] {
	case "bid":
		val := int(m["value"].(float64))
		if val <= r.highestBid || p.passed {
			return
		}
		p.bidDeclared = true
		p.bidValue = val
		r.highestBid = val
		r.highestBidder = p
		broadcast(p.room, map[string]any{
			"type":    "info",
			"message": fmt.Sprintf("Igrač %d licitira %d", p.id, val),
		})
		r.currentBidIndex = (r.currentBidIndex + 1) % 3
		r.passCount = 0
		r.players[r.currentBidIndex].conn.WriteJSON(map[string]any{
			"type":  "your_turn",
			"index": r.currentBidIndex,
		})
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
	case "kontra":
		if !r.auctionDone {
			// Kontra se može dati samo nakon licitacije
			return
		}
		if r.kontraStatus < 3 {
			r.kontraStatus++
			r.kontraBy = p.id
			r.kontraActive = true
			broadcast(p.room, map[string]any{
				"type":    "info",
				"message": fmt.Sprintf("Igrač %d kaže %s", p.id, []string{"kontra", "rekontra", "subkontra"}[r.kontraStatus-1]),
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
	case "prati":
		if r.prihvatili == 2 {
			if r.highestBid == 2 && r.kontraStatus == 0 {
				broadcast(p.room, map[string]any{
					"type":    "info",
					"message": "Igra od 2 bez kontre ne važi. Upisuje se refe i nova podela.",
				})
				for _, pl := range r.players {
					if pl.refe < r.maxRefe {
						pl.refe++
					}
				}
				time.Sleep(2 * time.Second)
				startGame(r)
			} else {
				r.potvrdaOdigravanja = true
				r.highestBidder.conn.WriteJSON(map[string]any{
					"type":    "potvrdi_igru",
					"message": "Potvrdi šta igraš ili najavi veću igru.",
				})
				broadcast(p.room, map[string]any{
					"type":    "info",
					"message": "Svi prate. Čeka se potvrda deklaranta.",
				})
			}
		} else {
			r.prihvatili++
			p.prihvatio = true
			broadcast(p.room, map[string]any{
				"type":    "info",
				"message": fmt.Sprintf("Igrač %d prati.", p.id),
			})
		}
	case "potvrda":
		if p != r.highestBidder || !r.potvrdaOdigravanja {
			return
		}
		igraStr, ok := m["value"].(string)
		if !ok {
			return
		}
		r.adut = igraStr
		r.potvrdaOdigravanja = false
		broadcast(p.room, map[string]any{
			"type":    "info",
			"message": fmt.Sprintf("Igrač %d potvrđuje: %s", p.id, r.adut),
		})
		for _, pl := range r.players {
			if pl != p {
				pl.conn.WriteJSON(map[string]any{
					"type":    "kontra_prompt",
					"message": "Da li daješ kontru?",
				})
			}
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
		// TODO: validacija da li je potez legalan, redosled igranja, čuvanje ruke itd.

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
}

// Pomoćna funkcija za slanje karata igraču (dummy implementacija)
func sendCards(p *Player) {
	p.conn.WriteJSON(map[string]any{
		"type":  "cards",
		"cards": p.cards,
	})
}

// Pomoćna funkcija za broadcast poruke u sobi
func broadcast(room string, msg map[string]any) {
	mu.Lock()
	defer mu.Unlock()
	r := rooms[room]
	for _, p := range r.players {
		p.conn.WriteJSON(msg)
	}
}

// Broadcast sa Room referencom i stringom
func broadcastMessage(r *Room, message string) {
	broadcast(r.players[0].room, map[string]any{
		"type":    "info",
		"message": message,
	})
}

func main() {
	// Ovdje tvoja inicijalizacija servera i websocket handleri
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/", serveHome)
	http.Handle("/cards/", http.StripPrefix("/cards/", http.FileServer(http.Dir("static/cards"))))
	fmt.Println("Server running at http://localhost:8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
func serveHome(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<!DOCTYPE html>
<html lang="sr">
<head>
  <meta charset="UTF-8">
  <title>Preferans</title>
  <style>
    #cardDisplay img {
      height: 120px;
      margin-right: 5px;
    }
  </style>
</head>
<body>
  <h2>Preferans test</h2>
  <button onclick="connect()">Poveži se</button>
  <button onclick="showCards()">Prikaži karte</button>
  <div id="buttons"></div>
  <div id="cardDisplay" style="margin-top:10px;"></div>
  <pre id="out"></pre>

  <script>
    let ws;
    let myIndex = -1;
    let myCards = [];

    function connect() {
      ws = new WebSocket("ws://localhost:8080/ws");
      ws.onmessage = e => {
        const data = JSON.parse(e.data);
        if (data.type === "cards") {
          myCards = data.cards;
          log("Karte su podeljene.");
        } else if (data.type === "info") {
          log("Info: " + data.message);
        } else if (data.type === "your_turn") {
          myIndex = data.index;
          showBidButtons();
        }
      };
    }

    function showCards() {
      const cardDiv = document.getElementById("cardDisplay");
      cardDiv.innerHTML = "";
      myCards.forEach(card => {
        const img = document.createElement("img");
        img.src = "/cards/" + cardToFilename(card);
        img.alt = card;
        cardDiv.appendChild(img);
      });
    }

    function cardToFilename(card) {
      const rank = card.slice(0, -1);
      const suitSymbol = card.slice(-1);
      const suitMap = { "♠": "spades", "♥": "hearts", "♦": "diamonds", "♣": "clubs" };
      return rank + "_" + suitMap[suitSymbol] + ".png";
    }

    function showBidButtons() {
      const btns = [2, 3, 4, 5, 6, 7];
      const names = ["2", "3", "4", "5", "betl", "sans"];
      const div = document.getElementById("buttons");
      div.innerHTML = "";
      btns.forEach((val, i) => {
        const b = document.createElement("button");
        b.textContent = names[i];
        b.onclick = () => bid(val);
        div.appendChild(b);
      });
      const igra = document.createElement("button");
      igra.textContent = "IGRA";
      igra.onclick = () => bid("igra");
      div.appendChild(igra);

	  const pass = document.createElement("button");
	  pass.textContent = "Pas";
      pass.onclick = () => ws.send(JSON.stringify({ type: "pass" }));
      div.appendChild(pass);

    }

    function bid(val) {
      ws.send(JSON.stringify({ type: "bid", value: val }));
      document.getElementById("buttons").innerHTML = "";
    }

    function log(msg) {
      document.getElementById("out").textContent += "\n" + msg;
    }
  </script>
</body>
</html>`)
}
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	defer conn.Close()

	player := &Player{conn: conn}
	roomID := assignToRoom(player)
	log.Printf("Player joined %s", roomID)

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			break
		}
		handleMessage(player, msg)
	}
}
