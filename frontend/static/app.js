const VALID_CHARS = " ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789':?!.-/$@#%";
const validSet = new Set(VALID_CHARS);

const validCharsEl = document.getElementById("valid-chars");
const inputEl = document.getElementById("message-input");
const displayLine = document.getElementById("display-line");
const displayText = document.getElementById("display-text");
const charCountEl = document.getElementById("char-count");
const messageHistoryEl = document.getElementById("message-history");

function normalizeInputText(value) {
    return (value || "").replace(/\u00A0/g, " ");
}

function getInputValue() {
    return normalizeInputText(inputEl.textContent || "");
}

function renderInputText(value) {
    const normalizedValue = normalizeInputText(value);
    inputEl.innerHTML = "";
    const chars = Array.from(normalizedValue || "");
    const fragment = document.createDocumentFragment();

    chars.forEach((ch) => {
        const span = document.createElement("span");
        span.className = "input-char";
        span.textContent = ch === " " ? "\u00A0" : ch;
        fragment.appendChild(span);
    });

    inputEl.appendChild(fragment);

    const selection = window.getSelection();
    const range = document.createRange();
    if (inputEl.childNodes.length === 0) {
        range.setStart(inputEl, 0);
    } else {
        range.setStartAfter(inputEl.lastChild);
    }
    range.collapse(true);
    selection.removeAllRanges();
    selection.addRange(range);
    inputEl.focus();
}

let previousValue = "";
const fadeTimers = new Map();

function buildValidCharsDisplay() {
    validCharsEl.innerHTML = "";
    const chars = Array.from(VALID_CHARS);
    const charsPerRow = Math.ceil(chars.length / 3);

    chars.forEach((ch, index) => {
        const span = document.createElement("span");
        span.className = "char";
        span.dataset.char = ch;
        span.textContent = ch === " " ? "\u00A0" : ch;
        validCharsEl.appendChild(span);

        if ((index + 1) % charsPerRow === 0 && index < chars.length - 1) {
            validCharsEl.appendChild(document.createElement("br"));
        }
    });
}

function highlightChar(ch) {
    const upperCh = ch.toUpperCase();
    const spans = validCharsEl.querySelectorAll(".char");
    spans.forEach((span) => {
        if (span.dataset.char === upperCh) {
            span.classList.add("active");
            if (fadeTimers.has(upperCh)) {
                clearTimeout(fadeTimers.get(upperCh));
            }
            fadeTimers.set(upperCh, setTimeout(() => {
                span.classList.remove("active");
                fadeTimers.delete(upperCh);
            }, 500));
        }
    });
}

function updateHighlights() {
    const currentValue = getInputValue().toUpperCase();
    const previousUpper = previousValue.toUpperCase();

    for (let i = 0; i < currentValue.length; i++) {
        const ch = currentValue[i];
        if (i >= previousUpper.length || ch !== previousUpper[i]) {
            highlightChar(ch);
        }
    }

    previousValue = getInputValue();
}

function filterInput() {
    const currentValue = getInputValue();
    const filtered = [...currentValue]
        .filter((ch) => validSet.has(ch.toUpperCase()))
        .join("")
        .toUpperCase();

    renderInputText(filtered);
    updateHighlights();
    updateCharCount();
}

function updateCharCount() {
    const count = getInputValue().length;
    charCountEl.textContent = count > 0 ? count + " characters" : "";
}

async function loadMessageHistory() {
    try {
        const res = await fetch("/api/history");
        if (res.ok) {
            const messages = await res.json();
            renderMessageHistory(messages);
        }
    } catch (err) {
        console.error("Failed to load message history:", err);
    }
}

function renderMessageHistory(messages) {
    if (!messages || messages.length === 0) {
        messageHistoryEl.innerHTML = "";
        return;
    }

    const table = document.createElement("table");
    table.className = "history-table";

    const thead = document.createElement("thead");
    thead.innerHTML = `
        <tr>
            <th>Time</th>
            <th>User</th>
            <th>Message</th>
        </tr>
    `;
    table.appendChild(thead);

    const tbody = document.createElement("tbody");
    messages.forEach((msg) => {
        const row = document.createElement("tr");

        const timeCell = document.createElement("td");
        timeCell.textContent = msg.time;

        const userCell = document.createElement("td");
        userCell.textContent = msg.user;

        const messageCell = document.createElement("td");
        const chars = Array.from(msg.message || "");
        chars.forEach((ch) => {
            const span = document.createElement("span");
            span.className = "display-char";
            span.textContent = ch === " " ? "\u00A0" : ch;
            messageCell.appendChild(span);
        });

        row.appendChild(timeCell);
        row.appendChild(userCell);
        row.appendChild(messageCell);
        tbody.appendChild(row);
    });
    table.appendChild(tbody);

    messageHistoryEl.innerHTML = "";
    messageHistoryEl.appendChild(table);
}

let statusTimeout;

function showStatus(text, type) {
    clearTimeout(statusTimeout);
    charCountEl.textContent = text;
    charCountEl.className = "char-count " + type;
    if (text) {
        statusTimeout = setTimeout(() => {
            charCountEl.className = "char-count";
            updateCharCount();
        }, 3000);
    }
}

async function sendMessage() {
    const payload = getInputValue().trim();
    if (!payload) return;

    try {
        const res = await fetch("/api/publish", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ payload }),
        });
        if (res.ok) {
            renderInputText("");
            previousValue = "";
            showStatus("\u2713 Sent", "success");
            loadMessageHistory();
        } else {
            const data = await res.json();
            showStatus("Error: " + (data.detail || res.statusText), "error");
        }
    } catch (err) {
        showStatus("Error: " + err.message, "error");
    }
}

function renderDisplayText(payload) {
    const normalizedPayload = normalizeInputText(payload);
    displayText.innerHTML = "";
    const chars = Array.from(normalizedPayload || "");
    const fragment = document.createDocumentFragment();

    chars.forEach((ch) => {
        const span = document.createElement("span");
        span.className = "display-char";
        span.textContent = ch === " " ? "\u00A0" : ch;
        fragment.appendChild(span);
    });

    displayText.appendChild(fragment);
}

function connectSSE() {
    const source = new EventSource("/api/stream");

    source.addEventListener("message", (event) => {
        const data = JSON.parse(event.data);
        renderDisplayText(data.payload);
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
loadMessageHistory();
