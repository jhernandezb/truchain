package vote

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/TruStory/truchain/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/assert"
)

func TestCreateVoteMsg(t *testing.T) {
	ctx, k, sk, ck, challengeKeeper, bankKeeper, _ := mockDB()

	h := NewHandler(k)
	assert.NotNil(t, h)

	storyID := createFakeStory(ctx, sk, ck)
	amount := sdk.NewCoin("trudex", sdk.NewInt(15))
	creator := sdk.AccAddress([]byte{1, 2})
	cnn, _ := url.Parse("http://www.cnn.com")
	evidence := []url.URL{*cnn}

	// give user some funds
	bankKeeper.AddCoins(ctx, creator, sdk.Coins{amount.Plus(amount)})

	argument := "test argument"
	_, err := challengeKeeper.Create(ctx, storyID, amount, argument, creator, evidence)
	assert.Nil(t, err)

	msg := NewCreateVoteMsg(storyID, amount, "valid comment", creator, evidence, true)
	assert.NotNil(t, msg)

	res := h(ctx, msg)
	idres := new(types.IDResult)
	_ = json.Unmarshal(res.Data, &idres)

	assert.Equal(t, int64(1), idres.ID)
}
