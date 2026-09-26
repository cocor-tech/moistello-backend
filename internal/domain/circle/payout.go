package circle

import (
	"math/rand"

	"github.com/moistello/backend/pkg/money"
)

func RandomOrder(seed int64, memberCount int) []int {
	order := make([]int, memberCount)
	for i := range order {
		order[i] = i
	}
	rng := rand.New(rand.NewSource(seed))
	rng.Shuffle(len(order), func(i, j int) {
		order[i], order[j] = order[j], order[i]
	})
	return order
}

func AuctionWinner(bids map[string]float64) (string, float64) {
	var maxBid float64
	var winner string
	for user, bid := range bids {
		if bid > maxBid {
			maxBid = bid
			winner = user
		}
	}
	return winner, maxBid
}

func VoteTally(votes map[string]int) string {
	var maxVotes int
	var winner string
	for user, cnt := range votes {
		if cnt > maxVotes {
			maxVotes = cnt
			winner = user
		}
	}
	return winner
}

// PayoutPool returns the total pool for a round: every member's contribution
// plus any late penalties collected, computed exactly in stroops.
func PayoutPool(contribution money.Money, memberCount int, latePenalties money.Money) (money.Money, error) {
	pool, err := contribution.MulInt(int64(memberCount))
	if err != nil {
		return money.Money{}, err
	}
	return pool.Add(latePenalties)
}

// CalculatePayout is the float adapter over PayoutPool kept for callers that
// still carry float64 circle fields. Non-finite inputs yield zero.
func CalculatePayout(contributionAmount float64, memberCount int, roundNumber int, latePenalties float64) float64 {
	contribution, err := money.FromFloat64(contributionAmount)
	if err != nil {
		return 0
	}
	penalties, err := money.FromFloat64(latePenalties)
	if err != nil {
		return 0
	}
	pool, err := PayoutPool(contribution, memberCount, penalties)
	if err != nil {
		return 0
	}
	return pool.Float64()
}
