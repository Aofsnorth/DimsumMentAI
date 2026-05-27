# Pathfinder Debug Visualizer

3D debug UI for the Bedrock bot pathfinder. Connects to the bot process on port **8080**.

## Run

1. Start the bot (`go run ./cmd/bot/`) — visualizer server starts automatically.
2. In another terminal:

```bash
cd web/visualizer
npm install
npm run dev
```

3. Open http://localhost:5173

## Features

- **Live sync** — WebSocket ~12 Hz (position, path, movement state); blocks ~2 Hz in the same stream.
- **Click-to-preview** — Enable debug mode, click the ground grid to run A* from the bot without moving it. Cyan line = preview.
- **Walk here** — Send the bot to the clicked block using the same target as preview.
- **Live path** — Purple line = current `CurrentPath` from the bot.

## API (port 8080)

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/ws` | WebSocket | Live state + blocks |
| `/api/path/preview` | POST `{"x","y","z"}` | Preview path only |
| `/api/walk` | POST `{"x","y","z"}` | `WalkTo` block coords |
| `/api/blocks?radius=18` | GET | Solid blocks snapshot |
