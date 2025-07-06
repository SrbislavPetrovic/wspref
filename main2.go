// main.go — server za igru preferans
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
	conn        *websocket.Conn
	room        string
	cards       []string
	bidValue    int
	bidDeclared bool
	passed      bool
	id          int
	name        string
	prihvatio   bool // da li je prihvatio igru
	refe        int  // broj refea
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

func main() {
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
		if val <= r.highestBid {
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
		p.passed = true
		r.passCount++
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
		next := (r.currentBidIndex + 1) % 3
		for r.players[next].passed {
			next = (next + 1) % 3
		}
		r.currentBidIndex = next
		r.players[next].conn.WriteJSON(map[string]any{
			"type":  "your_turn",
			"index": next,
		})
	case "kontra":
		if r.kontraStatus < 3 {
			r.kontraStatus++
			r.kontraBy = p.id
			r.kontraActive = true
			broadcast(p.room, map[string]any{
				"type":    "info",
				"message": fmt.Sprintf("Igrač %d kaže %s", p.id, []string{"kontra", "rekontra", "subkontra"}[r.kontraStatus-1]),
			})
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
		// sad ostali mogu reći kontra
		for _, pl := range r.players {
			if pl != p {
				pl.conn.WriteJSON(map[string]any{
					"type":    "kontra_prompt",
					"message": "Da li daješ kontru?",
				})
			}
		}
	case "adut":
		// više se ne koristi jer imamo potvrdu
		return

	case "igra":
		if p.bidDeclared || p.passed {
			return // samo oni koji još nisu licitirali mogu da kažu "igra"
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
		// Uporedi jačinu igre
		if r.highestBidder == nil || jačaDeklaracija(val, r.highestBidder.declaredGame) {
			r.highestBidder = p
			r.highestBid = map[string]int{"igra": 8, "betl": 9, "sans": 10}[val]
		}
		// idi na sledećeg
		next := (r.currentBidIndex + 1) % 3
		r.currentBidIndex = next
		r.players[next].conn.WriteJSON(map[string]any{
			"type":  "your_turn",
			"index": next,
		})
	}
}

func jačaDeklaracija(a, b string) bool {
	redosled := map[string]int{"igra": 1, "betl": 2, "sans": 3}
	return redosled[a] > redosled[b]
}

func advanceBid(r *Room) {
	count := 0
	for _, p := range r.players {
		if p.bidDeclared {
			count++
		}
	}
	if count == 3 {
		if r.highestBidder != nil {
			r.auctionDone = true
			broadcast(r.players[0].room, map[string]any{
				"type":    "info",
				"message": fmt.Sprintf("Licitaciju dobio igrač %d sa ponudom %d", r.highestBidder.id, r.highestBid),
			})
		} else {
			broadcast(r.players[0].room, map[string]any{
				"type":    "info",
				"message": "Svi su rekli pas. Nova podela.",
			})
			time.Sleep(2 * time.Second)
			startGame(r)
		}
		return
	}
	for i := 1; i <= 3; i++ {
		ni := (r.currentBidIndex + i) % 3
		if !r.players[ni].bidDeclared {
			r.currentBidIndex = ni
			r.players[ni].conn.WriteJSON(map[string]any{
				"type":  "your_turn",
				"index": ni,
			})
			return
		}
	}
}

func sendCards(p *Player) {
	msg := map[string]interface{}{
		"type":  "cards",
		"cards": p.cards,
	}
	data, _ := json.Marshal(msg)
	p.conn.WriteMessage(websocket.TextMessage, data)
}

func broadcast(roomID string, msg map[string]interface{}) {
	data, _ := json.Marshal(msg)
	for _, p := range rooms[roomID].players {
		p.conn.WriteMessage(websocket.TextMessage, data)
	}
}

func broadcastMessage(r *Room, txt string) {
	broadcast(r.players[0].room, map[string]interface{}{
		"type":    "info",
		"message": txt,
	})
}
