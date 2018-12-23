package game

import (
	"time"

	app "github.com/TruStory/truchain/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// game states
// started
// * challenge threshold met
// * voting begins
// ended
// * voting period ended (24 hrs)
// * AND received min quorum (9+ votes)
// expired
// * voting period ended (24 hrs)
// * NOT received min quorum (9+ votes)
// * stake returned

// Game defines a validation game on a story
type Game struct {
	ID            int64          `json:"id"`
	StoryID       int64          `json:"story_id"`
	Creator       sdk.AccAddress `json:"creator"`
	ExpiresTime   time.Time      `json:"expires_time,omitempty"`
	EndTime       time.Time      `json:"end_time,omitempty"`
	ChallengePool sdk.Coin       `json:"challenge_pool,omitempty"`
	Started       bool           `json:"started,omitempty"`
	Timestamp     app.Timestamp  `json:"timestamp"`
}

// Params holds default parameters for a game
type Params struct {
	ChallengeToBackingRatio sdk.Dec       // % backings at which game begins
	MinChallengeStake       sdk.Int       // min amount required to challenge
	Expires                 time.Duration // time to expire if threshold not met
	VotingPeriod            time.Duration // length of challenge game / voting period
}

// DefaultParams creates a new MsgParams type with defaults
func DefaultParams() Params {
	return Params{
		ChallengeToBackingRatio: sdk.NewDecWithPrec(33, 2), // 33%
		MinChallengeStake:       sdk.NewInt(10),
		Expires:                 10 * 24 * time.Hour,
		VotingPeriod:            1 * 24 * time.Hour,
	}
}
