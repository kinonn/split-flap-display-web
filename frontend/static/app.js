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
const advancedForm = document.getElementById("advanced-form");

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

    const fragment = document.createDocumentFragment();
    messages.forEach((msg) => {
        const li = document.createElement("li");
        li.className = "history-item";

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
            loadMessageHistory();
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

advancedForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(advancedForm);
    const body = {
        text: (fd.get("text") || "").toString().trim(),
        priority: (fd.get("priority") || "normal").toString(),
    };
    const tdc = fd.get("targetDisplayCount");
    const dur = fd.get("displayDuration");
    if (tdc) body.targetDisplayCount = parseInt(tdc, 10);
    if (dur) body.displayDuration = parseInt(dur, 10);
    if (!body.text) return;
    try {
        const res = await fetch("/api/messages", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
        });
        if (res.ok) {
            advancedForm.reset();
            showStatus("\u2713 Added", "success");
        } else {
            const data = await res.json().catch(() => ({}));
            showStatus("Error: " + (data.detail || res.statusText), "error");
        }
    } catch (err) {
        showStatus("Error: " + err.message, "error");
    }
});

async function loadQueue() {
    try {
        const res = await fetch("/api/messages");
        if (res.ok) {
            const messages = await res.json();
            renderQueue(messages);
        }
    } catch (err) {
        console.error("Failed to load queue:", err);
    }
}

function renderQueue(messages) {
    queueListEl.innerHTML = "";
    if (!messages || messages.length === 0) {
        const li = document.createElement("li");
        li.className = "queue-empty";
        li.textContent = "Queue is empty.";
        queueListEl.appendChild(li);
        return;
    }

    messages.forEach((m) => {
        const li = document.createElement("li");
        li.className = "queue-item" + (m.priority === "high" ? " queue-item-high" : "");

        const textEl = document.createElement("span");
        textEl.className = "queue-msg";
        textEl.textContent = m.message;

        const metaEl = document.createElement("span");
        metaEl.className = "queue-meta";
        metaEl.textContent = `${m.displayCount}/${m.targetDisplayCount} · ${m.status}`;

        const removeBtn = document.createElement("button");
        removeBtn.type = "button";
        removeBtn.className = "queue-remove";
        removeBtn.textContent = "\u2715";
        removeBtn.title = "Remove from queue";
        removeBtn.addEventListener("click", () => removeMessage(m.id));

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
}

async function removeMessage(id) {
    try {
        const res = await fetch(`/api/messages/${id}`, { method: "DELETE" });
        if (res.ok) {
            loadQueue();
        } else {
            const data = await res.json().catch(() => ({}));
            showStatus("Error: " + (data.detail || res.statusText), "error");
        }
    } catch (err) {
        showStatus("Error: " + err.message, "error");
    }
}

function updateCurrent(message) {
    if (message) {
        renderDisplayChars(currentMsgEl, message.message);
    } else {
        currentMsgEl.textContent = "\u2014";
    }
}

function updateState(state, currentMessage) {
    stateBadgeEl.classList.remove("state-idle", "state-active");
    if (state === "Active" && currentMessage) {
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
            updateState(data.message ? "Active" : "Idle", data.message);
        } catch (e) {
            console.error("Bad current event:", e);
        }
    });

    source.addEventListener("queue", () => {
        loadQueue();
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
loadQueue();
loadMessageHistory();
