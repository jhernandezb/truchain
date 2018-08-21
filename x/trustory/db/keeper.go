package db

import (
	"encoding/binary"

	ts "github.com/TruStory/trucoin/x/trustory/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	amino "github.com/tendermint/go-amino"
)

// ============================================================================

// StoryKeeper data type
type StoryKeeper struct {
	StoryKey sdk.StoreKey
	Cdc      *amino.Codec
}

// NewStoryKeeper creates a new keeper with write and read access
func NewStoryKeeper(storyKey sdk.StoreKey, cdc *amino.Codec) StoryKeeper {
	return StoryKeeper{
		StoryKey: storyKey,
		Cdc:      cdc,
	}
}

// GetStory gets the story with the given id from the key-value store
func (sk StoryKeeper) GetStory(ctx sdk.Context, storyID int64) (ts.Story, sdk.Error) {
	store := ctx.KVStore(sk.StoryKey)
	key := generateKey(sk.StoryKey.String(), storyID)
	val := store.Get(key)
	if val == nil {
		return ts.Story{}, ts.ErrStoryNotFound(storyID)
	}
	story := &ts.Story{}
	err := sk.Cdc.UnmarshalBinary(val, story)
	if err != nil {
		panic(err)
	}
	return *story, nil
}

// AddStory adds a story to the key-value store
func (sk StoryKeeper) AddStory(
	ctx sdk.Context,
	body string,
	category ts.StoryCategory,
	creator sdk.AccAddress,
	storyType ts.StoryType) (int64, sdk.Error) {
	store := ctx.KVStore(sk.StoryKey)

	story := ts.Story{
		ID:           sk.newStoryID(store),
		Body:         body,
		Category:     category,
		CreatedBlock: ctx.BlockHeight(),
		Creator:      creator,
		State:        ts.Created,
		StoryType:    storyType,
	}

	val, err := sk.Cdc.MarshalBinary(story)
	if err != nil {
		panic(err)
	}

	key := generateKey(sk.StoryKey.String(), story.ID)
	store.Set(key, val)

	return story.ID, nil
}

// VoteStory saves a vote to a story
func (sk StoryKeeper) VoteStory(ctx sdk.Context, storyID int64, creator sdk.AccAddress, vote bool, stake sdk.Coin) sdk.Error {
	storyStore := ctx.KVStore(sk.StoryKey)
	storyKey := generateKey(sk.StoryKey.String(), storyID)
	storyVal := storyStore.Get(storyKey)
	if storyVal != nil {
		return ts.ErrStoryNotFound(storyID)
	}

	// get existing story
	story := &ts.Story{}
	err := sk.Cdc.UnmarshalBinary(storyVal, story)
	if err != nil {
		panic(err)
	}

	// TODO: add vote to story
	vote := ts.Vote{}

	// create new story with vote
	// replace old story with new one in store
	// check out https://github.com/cosmos/cosmos-academy/pull/59/files/#diff-e07e9be37dc27aff278c0ac2bba706faR165
	return nil
}

// ============================================================================

// VoteKeeper data type
type VoteKeeper struct {
	VoteKey sdk.StoreKey
	Cdc     *amino.Codec
}

// NewVoteKeeper creates a new keeper with write and read access
func NewVoteKeeper(voteKey sdk.StoreKey, cdc *amino.Codec) VoteKeeper {
	return VoteKeeper{
		VoteKey: voteKey,
		Cdc:     cdc,
	}
}

// GetVote gets a vote with the given id from the key-value store
func (k VoteKeeper) GetVote(ctx sdk.Context, voteID int64) (ts.Vote, sdk.Error) {
	store := ctx.KVStore(k.VoteKey)
	key := generateKey(k.VoteKey.String(), voteID)
	val := store.Get(key)
	if val == nil {
		return ts.Vote{}, ts.ErrVoteNotFound(voteID)
	}
	vote := &ts.Vote{}
	err := k.Cdc.UnmarshalBinary(val, vote)
	if err != nil {
		panic(err)
	}
	return *vote, nil
}

// ============================================================================

// newStoryID creates a new id for a story by incrementing the last story id by 1
func (sk StoryKeeper) newStoryID(store sdk.KVStore) int64 {
	lastStoryID := store.Get([]byte("StoryID"))
	if lastStoryID == nil {
		return 0
	}

	storyID := new(int64)
	err := sk.Cdc.UnmarshalBinary(lastStoryID, storyID)
	if err != nil {
		panic(err)
	}

	return (*storyID + 1)
}

// TODO: duplicated code, create interface
// Does vote need an ID?
// newVoteID creates a new id for a vote by incrementing the last vote id by 1
func (k VoteKeeper) newVoteID(store sdk.KVStore) int64 {
	lastID := store.Get([]byte("VoteID"))
	if lastID == nil {
		return 0
	}

	ID := new(int64)
	err := k.Cdc.UnmarshalBinary(lastID, ID)
	if err != nil {
		panic(err)
	}

	return (*ID + 1)
}

// generateKey creates a key of the form "keyName"|{id}
func generateKey(keyName string, id int64) []byte {
	var key []byte
	idBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(idBytes, uint64(id))
	key = []byte(keyName)
	key = append(key, idBytes...)
	return key
}

// newID creates a new id by incrementing the last id by 1
// func newID(keyName string, store sdk.KVStore) int64 {
// 	lastID := store.Get([]byte(keyName))
// 	if lastID == nil {
// 		return 0
// 	}
// 	ID := new(int64)
// 	err := sk.Cdc.UnmarshalBinary(lastID, ID)
// 	if err != nil {
// 		panic(err)
// 	}
// 	return (*ID + 1)
// }
