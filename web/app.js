(() => {
  const $ = (id) => document.getElementById(id);
  const landing = $("landing"),
    roomView = $("room-view"),
    message = $("message");
  let socket,
    map,
    watchID,
    selfID,
    token,
    roomCode,
    autoFit = true;
  const markers = new Map(),
    people = new Map(),
    colors = [
      "#62e0bd",
      "#ffb86b",
      "#8db5ff",
      "#d49cff",
      "#ff8294",
      "#fff078",
      "#66d4e8",
      "#f08ac7",
      "#a8d878",
      "#f2a574",
    ];

  async function request(path, options = {}) {
    const response = await fetch(path, {
      headers: { "Content-Type": "application/json" },
      ...options,
    });
    const result = await response.json().catch(() => ({}));
    if (!response.ok)
      throw new Error(result.error?.message || "Request failed");
    return result;
  }
  function showError(err) {
    message.textContent = err.message || "Something went wrong";
  }
  function enter(name, code, accessToken) {
    token = accessToken;
    roomCode = code;
    $("room-code").textContent = code;
    landing.classList.add("hidden");
    roomView.classList.remove("hidden");
    autoFit = true;
    map = L.map("map", { zoomControl: false }).setView([20, 0], 2);
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      attribution:
        '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a> contributors',
    }).addTo(map);
    L.control.zoom({ position: "bottomright" }).addTo(map);
    map.on("dragstart", () => {
      autoFit = false;
    });
    setInterval(refreshStale, 5000);
    connect(name);
  }
  function connect(name) {
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    socket = new WebSocket(
      `${scheme}//${location.host}/ws?token=${encodeURIComponent(token)}`,
    );
    socket.onopen = () => {
      $("location-status").textContent =
        "Connected · waiting for location permission";
      if (!navigator.geolocation) {
        $("location-status").textContent =
          "This browser does not support location sharing";
        return;
      }
      watchID = navigator.geolocation.watchPosition(
        (pos) => {
          if (socket.readyState !== WebSocket.OPEN) return;
          const locationData = {
            lat: pos.coords.latitude,
            lng: pos.coords.longitude,
            accuracy: pos.coords.accuracy,
          };
          socket.send(
            JSON.stringify({ type: "location_update", data: locationData }),
          );
          if (selfID)
            updateLocation({
              ...locationData,
              id: selfID,
              name,
              ts: new Date().toISOString(),
            });
          $("location-status").textContent = "Sharing your live location";
        },
        (err) => {
          $("location-status").textContent =
            err.code === 1
              ? "Location permission denied"
              : "Waiting for location…";
        },
        { enableHighAccuracy: true, maximumAge: 5000, timeout: 15000 },
      );
    };
    socket.onmessage = ({ data }) => {
      try {
        handleMessage(JSON.parse(data));
      } catch {}
    };
    socket.onclose = () => {
      if (!roomView.classList.contains("hidden"))
        $("location-status").textContent =
          "Disconnected · leave and rejoin to reconnect";
    };
    socket.onerror = () => {
      $("location-status").textContent = "Connection error";
    };
  }
  function handleMessage(msg) {
    const d = msg.data || {};
    if (msg.type === "init") {
      selfID = d.selfId;
      (d.members || []).forEach((m) => {
        people.set(m.id, m);
        if (m.hasLocation) updateLocation({ ...m, ts: m.ts });
      });
      renderPeople();
    } else if (msg.type === "member_joined") {
      people.set(d.id, d);
      renderPeople();
    } else if (msg.type === "member_left") {
      people.delete(d.id);
      if (markers.has(d.id)) {
        map.removeLayer(markers.get(d.id));
        markers.delete(d.id);
      }
      renderPeople();
    } else if (msg.type === "location") updateLocation(d);
    else if (msg.type === "error")
      $("location-status").textContent = d.message || "Could not join room";
  }
  function updateLocation(d) {
    people.set(d.id, { ...people.get(d.id), ...d });
    const index = [...people.keys()].indexOf(d.id),
      color = colors[index % colors.length];
    const icon = L.divIcon({
      className: "",
      html: `<div class="custom-marker" style="background:${color};width:16px;height:16px"></div>`,
      iconSize: [16, 16],
      iconAnchor: [8, 8],
    });
    if (markers.has(d.id))
      markers.get(d.id).setLatLng([d.lat, d.lng]).setIcon(icon);
    else
      markers.set(
        d.id,
        L.marker([d.lat, d.lng], { icon })
          .addTo(map)
          .bindTooltip(escapeHTML(d.name)),
      );
    renderPeople();
    if (d.id === selfID && autoFit) map.setView([d.lat, d.lng], 15);
  }
  function renderPeople() {
    const list = $("members");
    list.replaceChildren();
    $("member-count").textContent = `${people.size}/10`;
    for (const [id, p] of people) {
      const li = document.createElement("li");
      const dot = document.createElement("span");
      dot.className = "dot";
      dot.style.background =
        colors[[...people.keys()].indexOf(id) % colors.length];
      const label = document.createElement("span");
      label.textContent = p.name + (id === selfID ? " (you)" : "");
      li.append(dot, label);
      if (p.ts) {
        const time = document.createElement("span");
        time.className = "person-meta";
        time.textContent = lastSeen(p.ts);
        li.append(time);
      }
      list.append(li);
    }
  }
  function lastSeen(timestamp) {
    const seconds = Math.max(
      0,
      Math.floor((Date.now() - new Date(timestamp).getTime()) / 1000),
    );
    if (seconds < 10) return "live";
    if (seconds < 120) return `${seconds}s ago`;
    return `stale · ${Math.floor(seconds / 60)}m ago`;
  }
  function refreshStale() {
    if (!roomView.classList.contains("hidden")) renderPeople();
  }
  function escapeHTML(value) {
    const el = document.createElement("span");
    el.textContent = value;
    return el.innerHTML;
  }
  $("create").addEventListener("click", async () => {
    try {
      const password =
        prompt("Optional room password (leave blank for none):") || "";
      const created = await request("/api/rooms", {
        method: "POST",
        body: JSON.stringify({ password }),
      });
      const name = prompt("Choose your display name:");
      if (!name?.trim()) return;
      const joined = await request(`/api/rooms/${created.code}/join`, {
        method: "POST",
        body: JSON.stringify({ name, password }),
      });
      enter(name, created.code, joined.token);
    } catch (err) {
      showError(err);
    }
  });
  $("join-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    message.textContent = "";
    const code = $("code").value.trim().toUpperCase(),
      name = $("name").value.trim(),
      password = $("password").value;
    try {
      const joined = await request(
        `/api/rooms/${encodeURIComponent(code)}/join`,
        { method: "POST", body: JSON.stringify({ name, password }) },
      );
      enter(name, code, joined.token);
    } catch (err) {
      showError(err);
    }
  });
  $("copy").addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(roomCode);
      $("copy").textContent = "Copied";
      setTimeout(() => ($("copy").textContent = "Copy"), 1500);
    } catch {
      $("copy").textContent = roomCode;
    }
  });
  $("locate").addEventListener("click", () => {
    const points = [...markers.values()].map((m) => m.getLatLng());
    if (points.length === 1) map.setView(points[0], 15);
    else if (points.length) map.fitBounds(L.latLngBounds(points).pad(0.2));
  });
  $("leave").addEventListener("click", () => {
    if (watchID !== undefined) navigator.geolocation.clearWatch(watchID);
    if (socket) socket.close();
    location.href = "/";
  });
})();
