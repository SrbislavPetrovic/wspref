let mycards = [];

const socket = new WebSocket("ws://localhost:8080/ws");

let myPlayerId = null;

socket.onmessage = function(event) {
	const data = JSON.parse(event.data);
   // Ako je tvoj red za licitaciju i server šalje dostupne akcije
  if (data.type === "your_turn" && data.actions) {
    const container = document.getElementById("actions");
    container.innerHTML = ""; // očisti prethodne dugmiće

    data.actions.forEach(action => {
      const btn = document.createElement("button");
      btn.textContent = action;
      btn.onclick = () => {
        socket.send(JSON.stringify({ type: "bid", value: action }));
        container.innerHTML = ""; // skloni dugmiće nakon klika
      };
      container.appendChild(btn);
    });
  }
	if (data.type === "you_are") {
		myPlayerId = data.id;
		console.log("Ja sam igrač", myPlayerId);
	}

	if (data.type === "auction_turn" && myPlayerId !== null) {
		if (data.player === myPlayerId) {
			showAuctionButtons();
		}
	}
    if (data.type === "your_cards") {
        mycards = data.cards;
        console.log("Primljene karte:", mycards);
    }
    if (data.type === "auction_start" || data.type === "auction_turn") {
    if (data.player === myPlayerId) { // ← koristiš ID svog igrača
        showAuctionButtons();
    }
}
};

document.getElementById("show-cards-btn").addEventListener("click", () => {
    if (mycards.length === 0) {
        alert("Još uvek nema karata za prikaz!");
        return;
    }
    displayCards(mycards);
});

function displayCards(cards) {
    const container = document.getElementById("player-cards");
    container.innerHTML = "";

    cards.forEach(card => {
        const img = document.createElement("img");
        img.src = "/cards/" + cardToFilename(card);
        img.alt = card;
        img.style.height = "100px";
        img.style.marginRight = "5px";
        container.appendChild(img);
    });
}
const suitMap = {
    "♠": "spades",
    "♥": "hearts",
    "♦": "diamonds",
    "♣": "clubs"
};

function cardToFilename(card) {
    const rank = card.slice(0, card.length - 1); // npr. "10"
    const suit = card.slice(-1);                // npr. "♠"
    return `${rank}_${suitMap[suit]}.png`;      // npr. "10_spades.png"
}

function showAuctionButtons() {
    const container = document.getElementById("auction-buttons");
    container.innerHTML = "";

    const buttons = ["2", "3", "4", "5", "6", "7", "IGRA", "BETL", "SANS", "PAS"];

    buttons.forEach(label => {
        const btn = document.createElement("button");
        btn.textContent = label;
        btn.onclick = () => {
            socket.send(JSON.stringify({
                type: "licitacija",
                value: label.toLowerCase()
            }));
            container.innerHTML = ""; // ukloni dugmad posle klika
        };
        container.appendChild(btn);
    });
}