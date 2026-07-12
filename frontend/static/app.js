const VALID_CHARS = " ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789':?!.-/$@#%";
const validSet = new Set(VALID_CHARS);

const validCharsEl = document.getElementById("valid-chars");
const inputEl = document.getElementById("message-input");
const priorityToggleEl = document.getElementById("priority-toggle");
const quickSendForm = document.getElementById("quick-send");
const charCountEl = document.getElementById("char-count");
const messageHistoryEl = document.getElementById("message-history");
const queueListEl = document.getElementById("queue-list");
const currentMsgEl = document.getElementById("current-msg");
const stateBadgeEl = document.getElementById("state-badge");

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

// Authoritative state, kept in sync with the server via SSE.
let lastCurrent = null;
let lastQueue = [];
let lastHistory = [];
// Last payload reported by the physical display via the
// splitflap/splitflap/state MQTT topic. This drives the "Now showing"
// text so it reflects what the display is *actually* showing, including
// while the scheduler is IDLE.
let lastDisplayState = null;

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

function renderDisplayChars(targetEl, text) {
    const normalized = normalizeInputText(text);
    targetEl.innerHTML = "";
    const chars = Array.from(normalized || "");
    const fragment = document.createDocumentFragment();
    chars.forEach((ch) => {
        const span = document.createElement("span");
        span.className = "display-char";
        span.textContent = ch === " " ? "\u00A0" : ch;
        fragment.appendChild(span);
    });
    targetEl.appendChild(fragment);
}

function renderMessageHistory(messages) {
    lastHistory = messages || [];
    if (!messages || messages.length === 0) {
        messageHistoryEl.innerHTML = "";
        return;
    }

    const fragment = document.createDocumentFragment();
    messages.forEach((msg) => {
        const li = document.createElement("li");
        li.className = "history-item" + (msg.priority === "high" ? " history-item-high" : "");

        const timeEl = document.createElement("span");
        timeEl.className = "history-time";
        timeEl.textContent = msg.time;

        const userEl = document.createElement("span");
        userEl.className = "history-user";
        userEl.textContent = msg.user;

        const msgEl = document.createElement("span");
        msgEl.className = "history-msg";
        renderDisplayChars(msgEl, msg.message);

        const prioEl = document.createElement("span");
        if (msg.priority === "high") {
            prioEl.className = "priority-badge";
            prioEl.textContent = "HIGH";
        }

        li.appendChild(timeEl);
        li.appendChild(userEl);
        li.appendChild(msgEl);
        if (msg.priority === "high") {
            li.appendChild(prioEl);
        }
        fragment.appendChild(li);
    });

    messageHistoryEl.innerHTML = "";
    messageHistoryEl.appendChild(fragment);
}

let statusTimeout;

function showStatus(text, type) {
    clearTimeout(statusTimeout);
    charCountEl.textContent = text;
    charCountEl.className = "char-count " + (type || "");
    if (text) {
        statusTimeout = setTimeout(() => {
            charCountEl.className = "char-count";
            updateCharCount();
        }, 3000);
    }
}

async function sendMessage(payload, priority) {
    if (!payload) return;
    try {
        const res = await fetch("/api/publish", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ payload, priority }),
        });
        if (res.ok) {
            renderInputText("");
            previousValue = "";
            priorityToggleEl.checked = false;
            showStatus("\u2713 Queued", "success");
            // No need to refetch — the server's SSE `queue` and `history`
            // events will deliver the new state.
        } else {
            const data = await res.json().catch(() => ({}));
            showStatus("Error: " + (data.detail || res.statusText), "error");
        }
    } catch (err) {
        showStatus("Error: " + err.message, "error");
    }
}

quickSendForm.addEventListener("submit", (e) => {
    e.preventDefault();
    const payload = getInputValue().trim();
    const priority = priorityToggleEl.checked ? "high" : "normal";
    sendMessage(payload, priority);
});

function renderQueue(messages) {
    lastQueue = messages || [];
    queueListEl.innerHTML = "";
    if (!messages || messages.length === 0) {
        const li = document.createElement("li");
        li.className = "queue-empty";
        li.textContent = "Queue is empty.";
        queueListEl.appendChild(li);
        recomputeState();
        return;
    }

    messages.forEach((m) => {
        const li = document.createElement("li");
        li.className = "queue-item" + (m.priority === "high" ? " queue-item-high" : "");

        const timeEl = document.createElement("span");
        timeEl.className = "queue-time";
        timeEl.textContent = m.lastDisplayedTime || "\u2014";

        const userEl = document.createElement("span");
        userEl.className = "queue-user";
        userEl.textContent = m.user || "unknown";

        const textEl = document.createElement("span");
        textEl.className = "queue-msg";
        renderDisplayChars(textEl, m.message);

        const metaEl = document.createElement("span");
        metaEl.className = "queue-meta";
        metaEl.textContent = `${m.displayCount}/${m.targetDisplayCount} \u00b7 ${m.status}`;

        const removeBtn = document.createElement("button");
        removeBtn.type = "button";
        removeBtn.className = "queue-remove";
        removeBtn.textContent = "\u2715";
        removeBtn.title = "Remove from queue";
        removeBtn.addEventListener("click", () => removeMessage(m.id));

        li.appendChild(timeEl);
        li.appendChild(userEl);
        li.appendChild(textEl);
        if (m.priority === "high") {
            const badge = document.createElement("span");
            badge.className = "priority-badge";
            badge.textContent = "HIGH";
            li.appendChild(badge);
        }
        li.appendChild(metaEl);
        li.appendChild(removeBtn);
        queueListEl.appendChild(li);
    });
    recomputeState();
}

async function removeMessage(id) {
    try {
        const res = await fetch(`/api/messages/${id}`, { method: "DELETE" });
        if (!res.ok) {
            const data = await res.json().catch(() => ({}));
            showStatus("Error: " + (data.detail || res.statusText), "error");
        }
        // On success, the server pushes a `queue` SSE event with the new
        // snapshot — no explicit refresh needed.
    } catch (err) {
        showStatus("Error: " + err.message, "error");
    }
}

function updateCurrent(message) {
    lastCurrent = message;
    // NOTE: we intentionally do NOT touch currentMsgEl here. The "Now
    // showing" text is driven by `updateDisplayState` (the
    // splitflap/splitflap/state feedback) so it persists across IDLE
    // transitions instead of being overwritten by the em-dash.
    recomputeState();
}

function updateDisplayState(payload) {
    if (typeof payload !== "string") {
        return;
    }
    lastDisplayState = payload;
    renderDisplayChars(currentMsgEl, payload);
}

function recomputeState() {
    const active = lastCurrent !== null || (lastQueue && lastQueue.length > 0);
    stateBadgeEl.classList.remove("state-idle", "state-active");
    if (active) {
        stateBadgeEl.classList.add("state-active");
        stateBadgeEl.textContent = "Active";
    } else {
        stateBadgeEl.classList.add("state-idle");
        stateBadgeEl.textContent = "Idle";
    }
}

function connectSchedulerSSE() {
    const source = new EventSource("/api/scheduler/stream");

    source.addEventListener("current", (event) => {
        try {
            const data = JSON.parse(event.data);
            updateCurrent(data.message);
        } catch (e) {
            console.error("Bad current event:", e);
        }
    });

    source.addEventListener("display-state", (event) => {
        try {
            const data = JSON.parse(event.data);
            // Wire format: {type: "display-state", message: {message: <payload>}}
            if (data && data.message && typeof data.message.message === "string") {
                updateDisplayState(data.message.message);
            }
        } catch (e) {
            console.error("Bad display-state event:", e);
        }
    });

    source.addEventListener("queue", (event) => {
        try {
            const data = JSON.parse(event.data);
            renderQueue(data.messages || []);
        } catch (e) {
            console.error("Bad queue event:", e);
        }
    });

    source.addEventListener("history", (event) => {
        try {
            const data = JSON.parse(event.data);
            renderMessageHistory(data.messages || []);
        } catch (e) {
            console.error("Bad history event:", e);
        }
    });

    source.onerror = () => {
        source.close();
        setTimeout(connectSchedulerSSE, 3000);
    };
}

inputEl.addEventListener("input", filterInput);
inputEl.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        const payload = getInputValue().trim();
        const priority = priorityToggleEl.checked ? "high" : "normal";
        sendMessage(payload, priority);
    }
});

buildValidCharsDisplay();
connectSchedulerSSE();
