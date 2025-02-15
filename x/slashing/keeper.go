package slashing

import (
	"fmt"

	app "github.com/TruStory/truchain/types"
	"github.com/TruStory/truchain/x/account"
	"github.com/TruStory/truchain/x/bank"
	"github.com/TruStory/truchain/x/claim"
	"github.com/TruStory/truchain/x/staking"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/store/gaskv"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/params"
	log "github.com/tendermint/tendermint/libs/log"
)

// Keeper is the model object for the package slashing module
type Keeper struct {
	storeKey   sdk.StoreKey
	codec      *codec.Codec
	paramStore params.Subspace

	bankKeeper    bank.Keeper
	stakingKeeper staking.Keeper
	accountKeeper account.Keeper
	claimKeeper   claim.Keeper
}

// NewKeeper creates a new keeper of the slashing Keeper
func NewKeeper(
	storeKey sdk.StoreKey, paramStore params.Subspace, codec *codec.Codec,
	bankKeeper bank.Keeper, stakingKeeper staking.Keeper, accountKeeper account.Keeper, claimKeeper claim.Keeper,
) Keeper {
	return Keeper{
		storeKey,
		codec,
		paramStore.WithKeyTable(ParamKeyTable()),
		bankKeeper,
		stakingKeeper,
		accountKeeper,
		claimKeeper,
	}
}

// CreateSlash creates a new slash on an argument (mark as "Unhelpful" in app)
func (k Keeper) CreateSlash(ctx sdk.Context,
	argumentID uint64,
	slashType SlashType,
	slashReason SlashReason,
	slashDetailedReason string,
	creator sdk.AccAddress) (slash Slash, results []PunishmentResult, err sdk.Error) {

	logger := k.Logger(ctx)
	results = make([]PunishmentResult, 0)
	err = k.validateParams(ctx, argumentID, slashDetailedReason, creator)
	if err != nil {
		return
	}

	slashID, err := k.slashID(ctx)
	if err != nil {
		return
	}

	slash = Slash{
		ID:             slashID,
		ArgumentID:     argumentID,
		Type:           slashType,
		Reason:         slashReason,
		DetailedReason: slashDetailedReason,
		Creator:        creator,
		CreatedTime:    ctx.BlockHeader().Time,
	}

	// persist the slash
	k.setSlash(ctx, slash)
	// increment the slash ID for the next slash
	k.setSlashID(ctx, slashID+1)
	// persist associations
	k.setCreatorSlash(ctx, creator, slashID)
	k.incrementSlashCount(ctx, argumentID)
	k.setArgumentSlash(ctx, argumentID, slashID)
	k.setArgumentSlasherSlash(ctx, argumentID, slashID, creator)

	err = k.stakingKeeper.DownvoteArgument(ctx, argumentID)
	if err != nil {
		return slash, results, err
	}

	slashCount := k.getSlashCount(ctx, argumentID)
	if slashCount >= k.GetParams(ctx).MinSlashCount || k.isAdmin(ctx, creator) {
		err = k.stakingKeeper.MarkUnhelpfulArgument(ctx, argumentID)
		if err != nil {
			return slash, results, err
		}
		results, err = k.punish(ctx, argumentID)
		if err != nil {
			return slash, results, err
		}
	}

	logger.Info(fmt.Sprintf("Created new slash: %s", slash.String()))

	return
}

func (k Keeper) refundStake(ctx sdk.Context, stake staking.Stake, communityID string) sdk.Error {
	if stake.Expired {
		return nil
	}
	var refundType bank.TransactionType

	switch stake.Type {
	case staking.StakeBacking:
		refundType = staking.TransactionBackingReturned
	case staking.StakeChallenge:
		refundType = staking.TransactionChallengeReturned
	case staking.StakeUpvote:
		refundType = staking.TransactionUpvoteReturned
	default:
		return staking.ErrCodeInvalidStakeType(stake.Type)
	}
	_, err := k.bankKeeper.AddCoin(ctx, stake.Creator, stake.Amount, stake.ArgumentID,
		refundType, WithCommunityID(communityID), FromModuleAccount(staking.UserStakesPoolName),
	)
	if err != nil {
		return err
	}
	return nil
}

func (k Keeper) punish(ctx sdk.Context, argumentID uint64) ([]PunishmentResult, sdk.Error) {
	stakingPool := sdk.NewCoin(app.StakeDenom, sdk.ZeroInt())
	var communityID string
	punishmentResults := make([]PunishmentResult, 0)
	for _, stake := range k.stakingKeeper.ArgumentStakes(ctx, argumentID) {
		communityID = stake.CommunityID
		stakingPool = stakingPool.Add(stake.Amount)
		err := k.refundStake(ctx, stake, communityID)
		if err != nil {
			return punishmentResults, err
		}
		if !stake.Expired {
			k.stakingKeeper.RemoveFromActiveStakeQueue(ctx, stake.ID, stake.EndTime)
			err := k.stakingKeeper.SetStakeExpired(ctx, stake.ID)
			if err != nil {
				return punishmentResults, err
			}
		}
		if stake.Expired && stake.Result != nil {
			punishmentResults, err := k.punishCreatorsWithExpiredStake(ctx, stake, communityID, punishmentResults)
			if err != nil {
				return punishmentResults, err
			}
		}
		slashMagnitude := int64(k.GetParams(ctx).SlashMagnitude)
		slashCoin := sdk.NewCoin(app.StakeDenom, stake.Amount.Amount.MulRaw(slashMagnitude))
		var slashTxType bank.TransactionType
		switch stake.Type {
		case staking.StakeUpvote:
			slashTxType = bank.TransactionStakeCuratorSlashed
		default:
			slashTxType = bank.TransactionStakeCreatorSlashed

		}
		_, amount, err := k.bankKeeper.SafeSubtractCoin(
			ctx,
			stake.Creator,
			slashCoin,
			stake.ID,
			slashTxType,
			WithCommunityID(communityID),
			ToModuleAccount(staking.UserRewardPoolName))
		punishmentResults = append(punishmentResults,
			PunishmentResult{Type: PunishmentStakeSlashed,
				AppAccAddress: stake.Creator,
				Coin:          amount,
			})
		if err != nil {
			return punishmentResults, err
		}

		argument, ok := k.stakingKeeper.Argument(ctx, argumentID)
		if !ok {
			return punishmentResults, ErrInvalidArgument(stake.ArgumentID)
		}

		if stake.Type == staking.StakeBacking {
			err = k.claimKeeper.SubtractBackingStake(ctx, argument.ClaimID, stake.Amount)
			if err != nil {
				return punishmentResults, err
			}
		}
		if stake.Type == staking.StakeChallenge {
			err = k.claimKeeper.SubtractChallengeStake(ctx, argument.ClaimID, stake.Amount)
			if err != nil {
				return punishmentResults, err
			}
		}

		// increment slash count for user (and jail if needed)
		jailed, err := k.accountKeeper.IncrementSlashCount(ctx, stake.Creator)
		if err != nil {
			return punishmentResults, err
		}

		k.Logger(ctx).Info(fmt.Sprintf("jailed: %+v", jailed))
		if jailed {
			punishmentResults = append(punishmentResults,
				PunishmentResult{
					Type:          PunishmentJailed,
					AppAccAddress: stake.Creator,
				},
			)
		}
	}

	if !stakingPool.IsPositive() {
		return punishmentResults, sdk.ErrInsufficientCoins("staking pool cannot be empty")
	}

	return k.rewardCurators(ctx, stakingPool, argumentID, communityID, punishmentResults)
}

func (k Keeper) punishCreatorsWithExpiredStake(ctx sdk.Context, stake staking.Stake, communityID string, punishmentResults []PunishmentResult) ([]PunishmentResult, sdk.Error) {
	switch stake.Result.Type {
	case staking.RewardResultArgumentCreation:
		// remove argument created interest from earned coins
		k.stakingKeeper.SubtractEarnedCoin(ctx,
			stake.Result.ArgumentCreator,
			communityID,
			stake.Result.ArgumentCreatorReward.Amount)
		_, amount, err := k.bankKeeper.SafeSubtractCoin(
			ctx,
			stake.Result.ArgumentCreator,
			stake.Result.ArgumentCreatorReward,
			stake.ID,
			bank.TransactionInterestArgumentCreationSlashed,
			WithCommunityID(communityID),
			ToModuleAccount(staking.UserRewardPoolName))
		punishmentResults = append(punishmentResults,
			PunishmentResult{Type: PunishmentInterestSlashed,
				AppAccAddress: stake.Result.ArgumentCreator,
				Coin:          amount,
			})
		if err != nil {
			return punishmentResults, err
		}
	case staking.RewardResultUpvoteSplit:
		// remove agree received interest from earned coins
		k.stakingKeeper.SubtractEarnedCoin(ctx,
			stake.Result.ArgumentCreator,
			communityID,
			stake.Result.ArgumentCreatorReward.Amount)
		_, amount, err := k.bankKeeper.SafeSubtractCoin(
			ctx,
			stake.Result.ArgumentCreator,
			stake.Result.ArgumentCreatorReward,
			stake.ID,
			bank.TransactionInterestUpvoteReceivedSlashed,
			WithCommunityID(communityID),
			ToModuleAccount(staking.UserRewardPoolName))
		if err != nil {
			return punishmentResults, err
		}
		punishmentResults = append(punishmentResults,
			PunishmentResult{Type: PunishmentInterestSlashed,
				AppAccAddress: stake.Result.ArgumentCreator,
				Coin:          amount,
			})
		// remove agree given interest from earned coins
		k.stakingKeeper.SubtractEarnedCoin(ctx,
			stake.Result.StakeCreator,
			communityID,
			stake.Result.StakeCreatorReward.Amount)
		_, amount, err = k.bankKeeper.SafeSubtractCoin(
			ctx,
			stake.Result.StakeCreator,
			stake.Result.StakeCreatorReward,
			stake.ID,
			bank.TransactionInterestUpvoteGivenSlashed,
			WithCommunityID(communityID),
			ToModuleAccount(staking.UserRewardPoolName))

		if err != nil {
			return punishmentResults, err
		}
		punishmentResults = append(punishmentResults,
			PunishmentResult{Type: PunishmentInterestSlashed,
				AppAccAddress: stake.Result.StakeCreator,
				Coin:          amount,
			})
	}

	return punishmentResults, nil
}

// reward curators who marked "unhelpful"
func (k Keeper) rewardCurators(ctx sdk.Context, stakingPool sdk.Coin, argumentID uint64, communityID string, punishmentResults []PunishmentResult) ([]PunishmentResult, sdk.Error) {
	curatorShareDec := k.GetParams(ctx).CuratorShare
	totalCuratorAmountDec := stakingPool.Amount.ToDec().Mul(curatorShareDec)

	slashes := k.ArgumentSlashes(ctx, argumentID)
	curatorAmount := totalCuratorAmountDec.QuoInt64(int64(len(slashes))).TruncateInt()
	curatorCoin := sdk.NewCoin(app.StakeDenom, curatorAmount)
	for _, slash := range slashes {
		_, err := k.bankKeeper.AddCoin(
			ctx,
			slash.Creator,
			curatorCoin,
			slash.ID,
			bank.TransactionCuratorReward,
			WithCommunityID(communityID),
			FromModuleAccount(staking.UserRewardPoolName))
		if err != nil {
			return punishmentResults, err
		}

		punishmentResults = append(punishmentResults,
			PunishmentResult{Type: PunishmentCuratorRewarded,
				AppAccAddress: slash.Creator,
				Coin:          curatorCoin,
			})
	}

	return punishmentResults, nil
}

// Slash returns a slash by its ID
func (k Keeper) Slash(ctx sdk.Context, id uint64) (slash Slash, err sdk.Error) {
	store := k.store(ctx)
	slashBytes := store.Get(key(id))
	if slashBytes == nil {
		return slash, ErrSlashNotFound(id)
	}
	k.codec.MustUnmarshalBinaryLengthPrefixed(slashBytes, &slash)

	return slash, nil
}

// Slashes gets all slashes from the KVStore
func (k Keeper) Slashes(ctx sdk.Context) (slashes []Slash) {
	store := k.store(ctx)
	iterator := sdk.KVStorePrefixIterator(store, SlashesKeyPrefix)

	return k.iterate(iterator)
}

// AddAdmin adds a new admin
func (k Keeper) AddAdmin(ctx sdk.Context, admin, creator sdk.AccAddress) (err sdk.Error) {
	params := k.GetParams(ctx)

	// first admin can be added without any authorisation
	if len(params.SlashAdmins) > 0 && !k.isAdmin(ctx, creator) {
		err = ErrAddressNotAuthorised()
	}

	// if already present, don't add again
	for _, currentAdmin := range params.SlashAdmins {
		if currentAdmin.Equals(admin) {
			return
		}
	}

	params.SlashAdmins = append(params.SlashAdmins, admin)

	k.SetParams(ctx, params)

	return
}

// RemoveAdmin removes an admin
func (k Keeper) RemoveAdmin(ctx sdk.Context, admin, remover sdk.AccAddress) (err sdk.Error) {
	if !k.isAdmin(ctx, remover) {
		err = ErrAddressNotAuthorised()
	}

	params := k.GetParams(ctx)
	for i, currentAdmin := range params.SlashAdmins {
		if currentAdmin.Equals(admin) {
			params.SlashAdmins = append(params.SlashAdmins[:i], params.SlashAdmins[i+1:]...)
		}
	}

	k.SetParams(ctx, params)

	return
}

// slashID gets the highest slash ID
func (k Keeper) slashID(ctx sdk.Context) (slashID uint64, err sdk.Error) {
	store := k.store(ctx)
	bz := store.Get(SlashIDKey)
	if bz == nil {
		return 0, ErrSlashNotFound(slashID)
	}
	k.codec.MustUnmarshalBinaryLengthPrefixed(bz, &slashID)
	return slashID, nil
}

// setSlash sets a slash in store
func (k Keeper) setSlash(ctx sdk.Context, slash Slash) {
	store := k.store(ctx)
	bz := k.codec.MustMarshalBinaryLengthPrefixed(slash)
	store.Set(key(slash.ID), bz)
}

// set the slash ID
func (k Keeper) setSlashID(ctx sdk.Context, slashID uint64) {
	store := k.store(ctx)
	bz := k.codec.MustMarshalBinaryLengthPrefixed(slashID)
	store.Set(SlashIDKey, bz)
}

// sets the association between the creator and the slash
func (k Keeper) setCreatorSlash(ctx sdk.Context, creator sdk.AccAddress, slashID uint64) {
	store := k.store(ctx)
	bz := k.codec.MustMarshalBinaryLengthPrefixed(slashID)
	store.Set(creatorSlashKey(creator, slashID), bz)
}

// increments the slash count for a given stake
func (k Keeper) incrementSlashCount(ctx sdk.Context, stakeID uint64) {
	k.setSlashCount(ctx, stakeID, uint64(k.getSlashCount(ctx, stakeID)+1))
}

// sets the association between the stake and the slash count
func (k Keeper) setSlashCount(ctx sdk.Context, stakeID uint64, count uint64) {
	store := k.store(ctx)
	bz := k.codec.MustMarshalBinaryLengthPrefixed(count)
	store.Set(slashCountKey(stakeID), bz)
}

// getSlashCount gets the number of slashes for a stake
func (k Keeper) getSlashCount(ctx sdk.Context, stakeID uint64) (count int) {
	store := k.store(ctx)
	bz := store.Get(slashCountKey(stakeID))
	if bz == nil {
		return 0
	}
	k.codec.MustUnmarshalBinaryLengthPrefixed(bz, &count)
	return count
}

// iterate iterates over the iterator
func (k Keeper) iterate(iterator sdk.Iterator) (slashes Slashes) {
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var slash Slash
		k.codec.MustUnmarshalBinaryLengthPrefixed(iterator.Value(), &slash)
		slashes = append(slashes, slash)
	}

	return
}

func (k Keeper) validateParams(ctx sdk.Context, argumentID uint64, detailedReason string, creator sdk.AccAddress) (err sdk.Error) {
	params := k.GetParams(ctx)

	a, ok := k.stakingKeeper.Argument(ctx, argumentID)
	if a.IsUnhelpful {
		return ErrAlreadyUnhelpful()
	}

	if !ok {
		return ErrInvalidArgument(argumentID)
	}

	if k.getSlashCount(ctx, argumentID) >= params.MinSlashCount {
		return ErrMaxSlashCountReached(argumentID)
	}

	if len(detailedReason) > params.MaxDetailedReasonLength {
		return ErrInvalidSlashReason(fmt.Sprintf("Detailed reason must be under %d chars.", params.MaxDetailedReasonLength))
	}
	if k.hasPreviouslySlashed(ctx, argumentID, creator) {
		return ErrAlreadySlashed()
	}

	// validating creator
	isAdmin := k.isAdmin(ctx, creator)
	hasEnoughCoins := k.hasEnoughEarnedStake(ctx, creator, params.SlashMinStake)

	if !isAdmin && !hasEnoughCoins {
		return ErrNotEnoughEarnedStake(creator)
	}

	return nil
}

func (k Keeper) hasEnoughEarnedStake(ctx sdk.Context, address sdk.AccAddress, requirement sdk.Coin) bool {
	totalStakeEarned := k.stakingKeeper.TotalEarnedCoins(ctx, address)

	return totalStakeEarned.GTE(requirement.Amount)
}

func (k Keeper) hasPreviouslySlashed(ctx sdk.Context, argumentID uint64, creator sdk.AccAddress) bool {
	slashes := k.ArgumentSlashes(ctx, argumentID)
	for _, slash := range slashes {
		if slash.Creator.Equals(creator) {
			return true
		}
	}

	return false
}

func (k Keeper) isAdmin(ctx sdk.Context, address sdk.AccAddress) bool {
	for _, admin := range k.GetParams(ctx).SlashAdmins {
		if address.Equals(admin) {
			return true
		}
	}
	return false
}

// setArgumentSlash sets a argument <-> slash association in store
func (k Keeper) setArgumentSlash(ctx sdk.Context, argumentID, slashID uint64) {
	bz := k.codec.MustMarshalBinaryLengthPrefixed(slashID)
	k.store(ctx).Set(argumentSlashKey(argumentID, slashID), bz)
}

func (k Keeper) ArgumentSlashes(ctx sdk.Context, argumentID uint64) []Slash {
	slashes := make([]Slash, 0)
	k.IterateArgumentSlashes(ctx, argumentID, func(slash Slash) bool {
		slashes = append(slashes, slash)
		return false
	})
	return slashes
}

func (k Keeper) IterateArgumentSlashes(ctx sdk.Context, argumentID uint64, cb slashCallback) {
	iterator := sdk.KVStorePrefixIterator(k.store(ctx), argumentSlashPrefix(argumentID))
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var slashID uint64
		k.codec.MustUnmarshalBinaryLengthPrefixed(iterator.Value(), &slashID)
		slash, err := k.Slash(ctx, slashID)
		if err != nil {
			panic(err)
		}
		if cb(slash) {
			break
		}
	}
}

func (k Keeper) setArgumentSlasherSlash(ctx sdk.Context, argumentID, slashID uint64, slasher sdk.AccAddress) {
	bz := k.codec.MustMarshalBinaryLengthPrefixed(slashID)
	k.store(ctx).Set(argumentSlasherSlashKey(argumentID, slasher, slashID), bz)
}

func (k Keeper) ArgumentSlasherSlashes(ctx sdk.Context, slasher sdk.AccAddress, argumentID uint64) []Slash {
	slashes := make([]Slash, 0)
	k.IterateArgumentSlasherSlashes(ctx, argumentID, slasher, func(slash Slash) bool {
		slashes = append(slashes, slash)
		return false
	})
	return slashes
}

type slashCallback func(slash Slash) (stop bool)

func (k Keeper) IterateArgumentSlasherSlashes(ctx sdk.Context, argumentID uint64, address sdk.AccAddress, cb slashCallback) {
	iterator := sdk.KVStorePrefixIterator(k.store(ctx), argumentSlasherPrefix(argumentID, address))
	defer iterator.Close()
	for ; iterator.Valid(); iterator.Next() {
		var slash Slash
		k.codec.MustUnmarshalBinaryLengthPrefixed(iterator.Value(), &slash)
		if cb(slash) {
			break
		}
	}
}

func (k Keeper) store(ctx sdk.Context) sdk.KVStore {
	return gaskv.NewStore(ctx.MultiStore().GetKVStore(k.storeKey), ctx.GasMeter(), app.KVGasConfig())
}

func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", ModuleName)
}
