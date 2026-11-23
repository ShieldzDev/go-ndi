# go-ndi (LocShare)

A small, self-hosted live location tracker for group outings and tours. Create a room, share its code with your group, and see everyone’s current position on a shared map—useful for keeping track of travel partners while exploring together.

No account is required. Room locations are kept in server memory and are not written to a database.

## Features

- Create a room with an optional password and invite others with a six-character code.
- Share live browser location and view room members on an OpenStreetMap-based map.
- See up to 10 people in a room, including when each location was last updated.
- Join from a phone or computer without installing an app.
- Room membership and location updates are temporary; inactive empty rooms are cleaned up automatically.

## Run locally

Requires the Go version specified in `go.mod`.

```sh
go run .
```

Open [http://localhost:8900](http://localhost:8900). Set `PORT` to use a different port. For example:

```sh
PORT=3000 go run .
```

For a stable signing key across restarts, set `SECRET` to at least 32 bytes. Without it, the server generates a temporary key on startup, so outstanding join tokens become invalid when the server restarts.

```sh
SECRET="$(openssl rand -hex 32)" go run .
```

To build and run the server:

```sh
go build -o go-ndi .
./go-ndi
```

## Use with your group

1. Open the app and create a room. Optionally set a room password.
2. Share the room code (and password, if set) with your tour partners.
3. Each person joins with the code and a display name, then allows location access in their browser.
4. The map updates as people share their positions. Select **Leave** to stop sharing and exit.

Location access generally requires HTTPS when accessing the app from another device. `localhost` is allowed for local testing. Use an HTTPS reverse proxy or host the app behind a platform that provides HTTPS; make sure it supports WebSocket connections.

## Privacy and room lifetime

Location sharing starts after a person joins and grants browser permission. A person’s location is shared with other members of that room. The server keeps room state in memory only; it does not persist room data to disk. An empty room is removed after 10 minutes, and rooms are removed after 12 hours without activity. A room supports up to 10 connected members.

Use a trusted server and share room codes/passwords only with the intended group. Anyone who can join a room can see its members’ shared locations.

## Development

Run the Go tests with:

```sh
go test ./...
```

The frontend is embedded into the Go server from `web/`. Leaflet and map tiles are loaded from external services (unpkg.com and OpenStreetMap), so browsers need network access to those services to display the map.
