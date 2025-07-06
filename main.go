// main.go — server za igru preferans
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ==== Strukture ====
type Player struct {
	conn     *websocket.Conn
	room     string
	cards    []string
	bidValue int  // vrednost ponude
	passed   bool // da li je odustao od licitacije
}

type Room struct {
	players         []*Player
	talon           []string
	bids            int // broj primljenih ponuda
	startIndex      int // indeks igrača koji prvi licitira i baca kartu
	currentBidIndex int // indeks igrača koji je trenutno na potezu za licitaciju
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

// Špil (preferans koristi 32 karte)
var deck = []string{
	"7♠", "8♠", "9♠", "10♠", "J♠", "Q♠", "K♠", "A♠",
	"7♥", "8♥", "9♥", "10♥", "J♥", "Q♥", "K♥", "A♥",
	"7♦", "8♦", "9♦", "10♦", "J♦", "Q♦", "K♦", "A♦",
	"7♣", "8♣", "9♣", "10♣", "J♣", "Q♣", "K♣", "A♣",
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
  <button onclick="sendBid()">Licitiraj 90</button>
  <button onclick="pass()">Pas</button>
  <div id="cardDisplay" style="margin-top:10px;"></div>
  <pre id="out"></pre>

  <script>
    let ws;
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
      const suitMap = {
        "♠": "spades",
        "♥": "hearts",
        "♦": "diamonds",
        "♣": "clubs"
      };
      return rank + "_" + suitMap[suitSymbol] + ".png";
    }

    function sendBid() {
      ws.send(JSON.stringify({ type: "bid", value: 2 }));
    }

    function pass() {
      ws.send(JSON.stringify({ type: "pass" }));
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

func handleMessage(p *Player, msg []byte) {
	var m map[string]interface{}
	if err := json.Unmarshal(msg, &m); err != nil {
		log.Println("Invalid message:", err)
		return
	}
	room := rooms[p.room]

	switch m["type"] {
	case "bid":
		if v, ok := m["value"].(float64); ok {
			p.bidValue = int(v)
			room.bids++
			broadcast(p.room, map[string]any{
				"type":    "info",
				"message": fmt.Sprintf("Igrač je licitirao %d", p.bidValue),
			})
			nextBidder(room)
			checkAuctionEnd(p.room)
		}
	case "pass":
		p.passed = true
		room.bids++
		broadcast(p.room, map[string]any{
			"type":    "info",
			"message": "Igrač je rekao pas",
		})
		nextBidder(room)
		checkAuctionEnd(p.room)
	}
}

func nextBidder(r *Room) {
	next := (r.currentBidIndex + 1) % 3
	for i := 0; i < 3; i++ {
		if !r.players[next].passed {
			r.currentBidIndex = next
			return
		}
		next = (next + 1) % 3
	}
}

func checkAuctionEnd(roomID string) {
	r := rooms[roomID]
	if r.bids == 3 {
		winner := findBidWinner(r)
		broadcast(roomID, map[string]any{
			"type":    "info",
			"message": fmt.Sprintf("Licitaciju dobio igrač sa %d", winner.bidValue),
		})
		// priprema sledeće runde
		r.startIndex = (r.startIndex + 1) % 3
	}
}

func findBidWinner(r *Room) *Player {
	var winner *Player
	max := 0
	for _, p := range r.players {
		if !p.passed && p.bidValue > max {
			max = p.bidValue
			winner = p
		}
	}
	return winner
}

func assignToRoom(p *Player) string {
	mu.Lock()
	defer mu.Unlock()

	for id, room := range rooms {
		if len(room.players) < 3 {
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
		sendCards(p)
	}
	r.talon = shuffled[30:]
	r.startIndex = rand.Intn(3)
	r.currentBidIndex = r.startIndex
	broadcastMessage(r, fmt.Sprintf("Licitaciju počinje igrač %d", r.startIndex))
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
