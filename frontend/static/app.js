const VALID_CHARS = " ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789':?!.-/$@#%";
const validSet = new Set(VALID_CHARS);

const validCharsEl = document.getElementById("valid-chars");
const inputEl = document.getElementById("message-input");
const displayLine = document.getElementById("display-line");
const displayText = document.getElementById("display-text");
const sendStatus = document.getElementById("send-status");

function buildValidCharsDisplay() {
    validCharsEl.innerHTML = "";
    for (const ch of VALID_CHARS) {
        const span = document.createElement("span");
        span.className = "char";
        span.dataset.char = ch;
        span.textContent = ch === " " ? "\u00A0" : ch;
        validCharsEl.appendChild(span);
    }
}

function updateHighlights() {
    const activeChars = new Set(inputEl.value.toUpperCase());
    const spans = validCharsEl.querySelectorAll(".char");
    spans.forEach((span) => {
        const ch = span.dataset.char;
        if (activeChars.has(ch)) {
            span.classList.add("active");
        } else {
            span.classList.remove("active");
        }
    });
}

function filterInput() {
    const filtered = [...inputEl.value]
        .filter((ch) => validSet.has(ch.toUpperCase()))
        .join("");
    if (filtered !== inputEl.value) {
        inputEl.value = filtered;
    }
    updateHighlights();
}

let statusTimeout;

function showStatus(text, type) {
    clearTimeout(statusTimeout);
    sendStatus.textContent = text;
    sendStatus.className = "send-status " + type;
    if (text) {
        statusTimeout = setTimeout(() => {
            sendStatus.textContent = "";
            sendStatus.className = "send-status";
        }, 3000);
    }
}

async function sendMessage() {
    const payload = inputEl.value.trim();
    if (!payload) return;

    try {
        const res = await fetch("/api/publish", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ payload }),
        });
        if (res.ok) {
            showStatus("\u2713 Sent", "success");
            inputEl.value = "";
            updateHighlights();
        } else {
            const data = await res.json();
            showStatus("Error: " + (data.detail || res.statusText), "error");
        }
    } catch (err) {
        showStatus("Error: " + err.message, "error");
    }
}

function connectSSE() {
    const source = new EventSource("/api/stream");

    source.addEventListener("message", (event) => {
        const data = JSON.parse(event.data);
        displayText.textContent = data.payload;
        displayLine.classList.remove("hidden");
    });

    source.onerror = () => {
        source.close();
        setTimeout(connectSSE, 3000);
    };
}

inputEl.addEventListener("input", filterInput);
inputEl.addEventListener("keydown", (e) => {
    if (e.key === "Enter") {
        e.preventDefault();
        sendMessage();
    }
});

buildValidCharsDisplay();
connectSSE();
