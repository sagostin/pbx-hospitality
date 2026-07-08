package api

import (
	"context"

	"github.com/sagostin/pbx-hospitality/internal/db"
	"github.com/sagostin/pbx-hospitality/internal/pms/tigertms"
)

// dbTokenResolver implements tigertms.TokenResolver against the DB.
// One instance per process; safe for concurrent use (the DB methods
// are gorm-backed).
type dbTokenResolver struct {
	database *db.DB
}

func newDBTokenResolver(database *db.DB) *dbTokenResolver {
	return &dbTokenResolver{database: database}
}

// ResolveTokenHash looks up an enabled token by SHA-256 hash and
// returns a ResolvedToken with the per-token auth context. Returns
// (nil, nil) when the hash doesn't match any enabled token.
func (r *dbTokenResolver) ResolveTokenHash(ctx context.Context, tokenHash string) (*tigertms.ResolvedToken, error) {
	row, err := r.database.LookupTenantInboundTokenByHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}
	return &tigertms.ResolvedToken{
		TokenID:    row.ID,
		TenantID:   row.TenantID,
		Strategy:   row.AuthStrategy,
		BearerHash: row.BearerHash,
		BasicUser:  row.BasicUser,
		BasicHash:  row.BasicHash,
	}, nil
}
