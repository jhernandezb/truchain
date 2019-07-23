package community

import (
	"encoding/json"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/assert"
)

// IDStringResult is when the result ID is a string
type IDStringResult struct {
	ID string `json:"id"`
}

func TestHandleMsgNewCommunity(t *testing.T) {
	ctx, keeper := mockDB()
	handler := NewHandler(keeper)
	assert.NotNil(t, handler) // assert handler is present

	name, id, description := getFakeCommunityParams()
	creator := keeper.GetParams(ctx).CommunityAdmins[0]
	msg := NewMsgNewCommunity(id, name, description, creator)
	assert.NotNil(t, msg) // assert msgs can be created

	result := handler(ctx, msg)
	idresult := new(IDStringResult)
	err := json.Unmarshal(result.Data, &idresult)
	assert.NoError(t, err)

	// TODO: if same community is created twice, it should actually throw an error
	result2 := handler(ctx, msg)
	idresult2 := new(IDStringResult)
	err = json.Unmarshal(result2.Data, &idresult2)
	assert.NoError(t, err)
}

func TestHandleMsgUpdateParams(t *testing.T) {
	ctx, keeper := mockDB()
	handler := NewHandler(keeper)
	assert.NotNil(t, handler) // assert handler is present

	updates := Params{
		MinIDLength: 20,
	}
	updatedFields := []string{"min_id_length"}
	updater := sdk.AccAddress([]byte{1, 2})
	msg := NewMsgUpdateParams(updates, updatedFields, updater)
	assert.NotNil(t, msg) // assert msgs can be created

	result := handler(ctx, msg)
	var success bool
	err := json.Unmarshal(result.Data, &success)
	assert.NoError(t, err)
}

func TestByzantineMsg(t *testing.T) {
	ctx, keeper := mockDB()

	handler := NewHandler(keeper)
	assert.NotNil(t, handler)

	res := handler(ctx, nil)
	assert.Equal(t, sdk.CodeUnknownRequest, res.Code)
	assert.Equal(t, sdk.CodespaceRoot, res.Codespace)
}
