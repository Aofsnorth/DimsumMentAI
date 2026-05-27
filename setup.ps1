# Minecraft Bedrock AI Bot - Windows Setup Script
# Author: FitraGum (2026)

Clear-Host
Write-Host "==================================================" -ForegroundColor Green
Write-Host "    MINECRAFT BEDROCK AI BOT - SETUP SCRIPT      " -ForegroundColor Green
Write-Host "==================================================" -ForegroundColor Green
Write-Host ""

# Step 1: Verify Go Installation
Write-Host "[1/4] Verifying Go Installation..." -ForegroundColor Cyan
$goVersion = go version 2>$null
if ($null -eq $goVersion) {
    Write-Host "ERROR: Go is not installed or not in your System PATH." -ForegroundColor Red
    Write-Host "Please install Go (v1.24+) from: https://go.dev/dl/" -ForegroundColor Yellow
    Write-Host "After installation, restart your terminal and run this script again." -ForegroundColor Yellow
    Exit 1
}
Write-Host "Found Go: $goVersion" -ForegroundColor Gray

# Step 2: Install Go dependencies
Write-Host "[2/4] Downloading and tidying dependencies..." -ForegroundColor Cyan
go mod tidy
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to run 'go mod tidy' to resolve Go dependencies." -ForegroundColor Red
    Exit 1
}
Write-Host "Dependencies verified successfully." -ForegroundColor Gray

# Step 3: Compiling Bot Binary
Write-Host "[3/4] Compiling bot binary..." -ForegroundColor Cyan
go build -o bot.exe ./cmd/bot
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to compile bot.exe." -ForegroundColor Red
    Exit 1
}
Write-Host "bot.exe compiled successfully!" -ForegroundColor Green

# Step 4: Configuration Setup
Write-Host "[4/4] Checking configuration files..." -ForegroundColor Cyan
$configPath = "configs/bot.yaml"
if (-Not (Test-Path $configPath)) {
    Write-Host "Config file configs/bot.yaml not found." -ForegroundColor Yellow
    # Create directory if missing
    New-Item -ItemType Directory -Path "configs" -Force | Out-Null
    
    # Create default config content
    $defaultConfig = @"
# ============================================
#  Minecraft Bedrock AI Bot Configuration
# ============================================

# --- Server Settings ---
server:
  host: "127.0.0.1"
  port: 19132
  offline: true # true = no Xbox Live auth (local server)

# --- Bot Identity ---
bot:
  name: "Luna" # In-game display name
  language: "Indonesian" # AI reply language, e.g. "English", "Indonesian", "Japanese"
  log_level: "info" # debug, info, warn, or error
  state_path: "data/bot_state.json" # Saves last standing position per server/name

# --- Skin & Geometry ---
skin:
  image_path: "imports/skins/image/Anime-thing-on-planetminecraft-com.png"
  arm_size: "slim" # "slim" (Alex) or "wide" (Steve)

# --- AI & LLM Settings ---
ai:
  provider: "nvidia" # "nvidia" or "none"
  model: "openai/gpt-oss-120b"
  api_key: "" # Leave empty to load from NVIDIA_API_KEY environment variable
  main_player: ".OnyxStygian" # Primary player/owner username
  respond_only_to_linked_player: false
  respond_only_when_tagged: false # true = only respond when tagged/named in chat (e.g. Luna or @Luna)
  custom_personality: "" # Custom LLM personality prompt override (leave empty to use default)
"@
    Set-Content -Path $configPath -Value $defaultConfig
    Write-Host "Created default config file at $configPath" -ForegroundColor Green
} else {
    Write-Host "Existing configuration file found at $configPath" -ForegroundColor Gray
}

Write-Host ""
Write-Host "==================================================" -ForegroundColor Green
Write-Host "             SETUP COMPLETED SUCCESSFULLY         " -ForegroundColor Green
Write-Host "==================================================" -ForegroundColor Green
Write-Host ""
Write-Host "To run your bot:" -ForegroundColor Yellow
Write-Host "  1. Set your NVIDIA API Key in environment (optional):" -ForegroundColor Gray
Write-Host "     `$env:NVIDIA_API_KEY = 'your-key-here'" -ForegroundColor Gray
Write-Host "  2. Start the bot:" -ForegroundColor Gray
Write-Host "     .\bot.exe" -ForegroundColor Gray
Write-Host ""
