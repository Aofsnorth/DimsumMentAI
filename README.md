# Minecraft Bedrock AI Bot

A comprehensive, state-of-the-art AI-powered bot client for **Minecraft Bedrock Edition**. Built in Go using the powerful [`gophertunnel`](https://github.com/sandertv/gophertunnel) library, this project features an autonomous agent capable of pathfinding, building, resource gathering, combat mitigation, and interactive conversation driven by **NVIDIA NIM LLMs**.

---

## 🌟 Key Features

### 🧠 LLM-Driven Intelligence
* **NVIDIA NIM Integration:** Dynamically processes chat requests and ambient gameplay states using models like `openai/gpt-oss-120b`.
* **State-Aware Prompts:** The AI client generates context-aware answers containing information about the bot's health, hunger, coordinates, held items, inventory contents, and owner position.
* **Autonomous Action Parser:** Translates natural language responses from the LLM into concrete in-game actions (e.g., `<action>stop</action>`, `<action>emote:wave,1</action>`).

### 🗺️ Advanced 3D Pathfinding & Navigation
* **A\* Pathfinding Solver:** High-performance pathfinding optimized for Minecraft's block grid.
* **Dynamic Hazard Detection:** Automatically marks coordinates as hazards when damage is taken, allowing the bot to dynamically recalculate paths and avoid danger.
* **Collision & Stuck Handling:** Intelligent stuck-recalculation thresholds and direct-movement fallbacks to bypass obstacles.
* **Natural Movement:** Smooth interpolation of look angles (yaw/pitch) to simulate human-like head movements and eliminate visual tremors.

### 🏗️ Modular Building System
* **Template-Based Construction:** Build pre-defined templates such as Japanese-style houses, medieval survival homes, or starter bases.
* **Building Planner & Placer:** Handles structural layout, block scanning, block placement transactions, and item acquisition.
* **Schematic Support:** Easily parse and place structures using custom JSON schematics.

### 🎒 Inventory & Action Controls
* **Live Equipment Management:** Auto-equip appropriate items, interact with containers, and drop resources on command.
* **Auto-Crafting Integration:** Interacts with Minecraft crafting recipes by caching shapeless and shaped server recipes for fast crafting.
* **Organic Emotes:** Interactive physical feedback including jumping, sneaking, spinning, wiggling, head nodding, and head shaking.

---

## 📂 Project Architecture

```
Minecraft Bedrock AI/
├── cmd/
│   └── bot/                  # Main application entry point (main.go)
├── configs/
│   ├── bot.yaml              # Core configuration file (Server, Skin, AI settings)
│   └── token.json            # Cached Xbox Live authentication tokens (git-ignored)
├── imports/
│   ├── geometry/             # Custom mob and player geometry templates
│   └── skins/                # Image files for player skins
├── internal/
│   ├── ai/                   # Nvidia client, chat history, prompt templates, action parser
│   ├── bot/                  # Main bot lifecycle, packet handlers, and sub-systems:
│   │   ├── building/         # Placer, scanner, schematic builder, structure templates
│   │   ├── combat/           # Threat detection and basic combat loop
│   │   ├── entity/           # World model, actor registry, hazard trackers
│   │   ├── gathering/        # Chopper, looter, miner, scaffolding controllers
│   │   ├── inventory/        # Container trackers and equipment managers
│   │   └── pathfinder/       # A* search nodes, heuristics, and navigation grid
│   ├── config/               # Configuration structs and YAML loader
│   ├── connection/           # Gophertunnel dialer and game server handshake
│   └── skin/                 # Custom skin builder, patches, and asset providers
└── LICENSE                   # MIT License
```

---

## 🚀 Getting Started

### Prerequisites
* **Go 1.24** or higher installed on your system.
* A running **Minecraft Bedrock Server** (tested on Bedrock Dedicated Server `1.26.x`).
* An **NVIDIA NIM API Key** (optional, for LLM chat capabilities).

### Installation
1. Clone this repository:
   ```bash
   git clone https://github.com/Aofsnorth/MinecraftBedrockAI.git
   cd MinecraftBedrockAI
   ```
2. Build the project:
   ```bash
   go build ./cmd/bot
   ```

### Configuration
1. Open the [configs/bot.yaml](configs/bot.yaml) file:
   * **`server`**: Specify your Minecraft server's host and port. Set `offline: true` if your local server does not have Xbox Live authentication enabled.
   * **`bot`**: Set the bot's in-game display name.
   * **`ai`**: Insert your Nvidia API key (`api_key`) and specify your main player name (`main_player`).
2. If you prefer to keep keys out of the config files, define the key in your environment variables:
   ```bash
   # Windows PowerShell
   $env:NVIDIA_API_KEY="your-api-key-here"
   
   # Linux/macOS
   export NVIDIA_API_KEY="your-api-key-here"
   ```

### Running the Bot
Run the compiled binary directly or execute via Go:
```bash
go run ./cmd/bot
```

---

## 🛠️ In-Game Commands & Interaction

The bot listens for chat messages from its configured `main_player`. It processes commands either directly or using AI inference:
* **Conversations:** Talk to the bot normally. It will generate responses using the LLM and carry out actions if requested (e.g., *"Come here"*, *"Stop moving"*, *"Build a house"*).
* **Direct Actions:**
  * `nod` / `shake` / `wave` / `wiggle` - Trigger physical emotes.
  * `stop` - Halt all active pathfinding or building routines.

---

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
