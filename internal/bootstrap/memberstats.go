package bootstrap

import (
	"context"

	"github.com/kissqianfu/aichat/internal/stats"
	"github.com/kissqianfu/aichat/internal/storage/db/repo"
)

// memberStatsStore 把 repo.MemberRepo 适配成 stats.Store（行类型转换）。
type memberStatsStore struct {
	inner *repo.MemberRepo
}

func (s memberStatsStore) BumpMember(ctx context.Context, groupID, openid, displayName string) error {
	return s.inner.BumpMember(ctx, groupID, openid, displayName)
}

func (s memberStatsStore) BumpPoke(ctx context.Context, groupID, src, dst string) error {
	return s.inner.BumpPoke(ctx, groupID, src, dst)
}

func (s memberStatsStore) BumpDaily(ctx context.Context, groupID, openid, day string, chars int) (bool, error) {
	return s.inner.BumpDaily(ctx, groupID, openid, day, chars)
}

func (s memberStatsStore) BumpReplyHit(ctx context.Context, groupID, openid, day string) error {
	return s.inner.BumpReplyHit(ctx, groupID, openid, day)
}

func (s memberStatsStore) BumpGroupDay(ctx context.Context, groupID, day string, firstToday bool) error {
	return s.inner.BumpGroupDay(ctx, groupID, day, firstToday)
}

func (s memberStatsStore) BumpGroupReply(ctx context.Context, groupID, day string) error {
	return s.inner.BumpGroupReply(ctx, groupID, day)
}

func (s memberStatsStore) TopActive(ctx context.Context, groupID string, days, n int) ([]stats.ActiveRow, error) {
	rows, err := s.inner.TopActive(ctx, groupID, days, n)
	if err != nil {
		return nil, err
	}
	out := make([]stats.ActiveRow, len(rows))
	for i, r := range rows {
		out[i] = stats.ActiveRow{OpenID: r.OpenID, MsgCount: r.MsgCount}
	}
	return out, nil
}

func (s memberStatsStore) TopPairsWith(ctx context.Context, groupID, openid string, n int) ([]stats.PairRow, error) {
	rows, err := s.inner.TopPairsWith(ctx, groupID, openid, n)
	if err != nil {
		return nil, err
	}
	out := make([]stats.PairRow, len(rows))
	for i, r := range rows {
		out[i] = stats.PairRow{Src: r.Src, Dst: r.Dst, Hits: r.Hits}
	}
	return out, nil
}

func (s memberStatsStore) DisplayNameOf(ctx context.Context, groupID, openid string) (string, error) {
	return s.inner.DisplayNameOf(ctx, groupID, openid)
}

func (s memberStatsStore) PruneDaily(ctx context.Context, keepDays int) (int64, error) {
	return s.inner.PruneDaily(ctx, keepDays)
}
