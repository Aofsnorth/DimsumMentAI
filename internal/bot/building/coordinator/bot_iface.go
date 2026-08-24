package coordinator

import "bedrock-ai/internal/bot/building/common"

// BotInterface is the contract the building coordinator needs from the main
// Bot struct. It is the canonical definition shared across the building
// subsystem in building/common — defining it again here would duplicate the
// contract and let the two copies drift, so this alias re-exports the single
// source of truth.
type BotInterface = common.BotInterface
