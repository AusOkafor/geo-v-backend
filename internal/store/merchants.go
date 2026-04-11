package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Merchant mirrors the merchants table.
type Merchant struct {
	ID                 int64
	ShopDomain         string
	AccessTokenEnc     string
	Scope              string
	Plan               string
	Active             bool
	BrandName          string   // derived from shop domain or stored separately
	Category           string   // product category — set during onboarding
	PricePositioning   string   // budget | mid-range | premium | luxury
	UniqueSellingPoint string   // max 200 chars; used in fix/pitch generation
	SocialLinks        []string // sameAs URLs (Instagram, TikTok, etc.)
	InstalledAt        time.Time
	UninstalledAt      *time.Time
}

// GetMerchant fetches a merchant by primary key.
func GetMerchant(ctx context.Context, db *pgxpool.Pool, id int64) (*Merchant, error) {
	var m Merchant
	var dbBrandName, dbCategory, dbPricePositioning, dbUniqueSellingPoint *string
	err := db.QueryRow(ctx, `
		SELECT id, shop_domain, access_token_enc, scope, plan, active, installed_at, uninstalled_at,
		       brand_name, category, price_positioning, unique_selling_point, social_links
		FROM merchants WHERE id = $1
	`, id).Scan(
		&m.ID, &m.ShopDomain, &m.AccessTokenEnc, &m.Scope,
		&m.Plan, &m.Active, &m.InstalledAt, &m.UninstalledAt,
		&dbBrandName, &dbCategory, &dbPricePositioning, &dbUniqueSellingPoint, &m.SocialLinks,
	)
	if err != nil {
		return nil, fmt.Errorf("store.GetMerchant: %w", err)
	}
	if dbBrandName != nil && *dbBrandName != "" {
		m.BrandName = *dbBrandName
	} else {
		m.BrandName = shopDomainToBrand(m.ShopDomain)
	}
	if dbCategory != nil {
		m.Category = *dbCategory
	}
	if dbPricePositioning != nil {
		m.PricePositioning = *dbPricePositioning
	}
	if dbUniqueSellingPoint != nil {
		m.UniqueSellingPoint = *dbUniqueSellingPoint
	}
	if m.SocialLinks == nil {
		m.SocialLinks = []string{}
	}
	return &m, nil
}

// GetMerchantByDomain fetches a merchant by Shopify shop domain.
func GetMerchantByDomain(ctx context.Context, db *pgxpool.Pool, domain string) (*Merchant, error) {
	var m Merchant
	var dbBrandName, dbCategory, dbPricePositioning, dbUniqueSellingPoint *string
	err := db.QueryRow(ctx, `
		SELECT id, shop_domain, access_token_enc, scope, plan, active, installed_at, uninstalled_at,
		       brand_name, category, price_positioning, unique_selling_point, social_links
		FROM merchants WHERE shop_domain = $1
	`, domain).Scan(
		&m.ID, &m.ShopDomain, &m.AccessTokenEnc, &m.Scope,
		&m.Plan, &m.Active, &m.InstalledAt, &m.UninstalledAt,
		&dbBrandName, &dbCategory, &dbPricePositioning, &dbUniqueSellingPoint, &m.SocialLinks,
	)
	if err != nil {
		return nil, fmt.Errorf("store.GetMerchantByDomain: %w", err)
	}
	if dbBrandName != nil && *dbBrandName != "" {
		m.BrandName = *dbBrandName
	} else {
		m.BrandName = shopDomainToBrand(m.ShopDomain)
	}
	if dbCategory != nil {
		m.Category = *dbCategory
	}
	if dbPricePositioning != nil {
		m.PricePositioning = *dbPricePositioning
	}
	if dbUniqueSellingPoint != nil {
		m.UniqueSellingPoint = *dbUniqueSellingPoint
	}
	if m.SocialLinks == nil {
		m.SocialLinks = []string{}
	}
	return &m, nil
}

// UpdateSocialLinks saves the merchant's sameAs social profile URLs.
func UpdateSocialLinks(ctx context.Context, db *pgxpool.Pool, id int64, links []string) error {
	_, err := db.Exec(ctx, `
		UPDATE merchants SET social_links = $1, updated_at = now()
		WHERE id = $2
	`, links, id)
	return err
}

// UpdateMerchantProfile saves the merchant's brand name, product category, and brand profile fields.
func UpdateMerchantProfile(ctx context.Context, db *pgxpool.Pool, id int64, brandName, category, pricePositioning, uniqueSellingPoint string) error {
	_, err := db.Exec(ctx, `
		UPDATE merchants
		SET brand_name = $1, category = $2, price_positioning = $3, unique_selling_point = $4, updated_at = now()
		WHERE id = $5
	`, brandName, category, pricePositioning, uniqueSellingPoint, id)
	return err
}

// GetActiveMerchants returns all merchants with active=true.
func GetActiveMerchants(ctx context.Context, db *pgxpool.Pool) ([]Merchant, error) {
	rows, err := db.Query(ctx, `
		SELECT id, shop_domain, access_token_enc, scope, plan, active, installed_at, uninstalled_at
		FROM merchants WHERE active = true
	`)
	if err != nil {
		return nil, fmt.Errorf("store.GetActiveMerchants: %w", err)
	}
	defer rows.Close()

	var merchants []Merchant
	for rows.Next() {
		var m Merchant
		if err := rows.Scan(
			&m.ID, &m.ShopDomain, &m.AccessTokenEnc, &m.Scope,
			&m.Plan, &m.Active, &m.InstalledAt, &m.UninstalledAt,
		); err != nil {
			return nil, err
		}
		m.BrandName = shopDomainToBrand(m.ShopDomain)
		merchants = append(merchants, m)
	}
	return merchants, rows.Err()
}

// UpsertMerchantParams are the fields needed to install/update a merchant.
type UpsertMerchantParams struct {
	ShopDomain     string
	AccessTokenEnc string
	Scope          string
}

// UpsertMerchant inserts or updates a merchant record on install/reinstall.
func UpsertMerchant(ctx context.Context, db *pgxpool.Pool, p UpsertMerchantParams) (*Merchant, error) {
	var m Merchant
	err := db.QueryRow(ctx, `
		INSERT INTO merchants (shop_domain, access_token_enc, scope, active)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (shop_domain) DO UPDATE SET
			access_token_enc = EXCLUDED.access_token_enc,
			scope            = EXCLUDED.scope,
			active           = true,
			uninstalled_at   = NULL,
			updated_at       = now()
		RETURNING id, shop_domain, access_token_enc, scope, plan, active, installed_at, uninstalled_at
	`, p.ShopDomain, p.AccessTokenEnc, p.Scope).Scan(
		&m.ID, &m.ShopDomain, &m.AccessTokenEnc, &m.Scope,
		&m.Plan, &m.Active, &m.InstalledAt, &m.UninstalledAt,
	)
	if err != nil {
		return nil, fmt.Errorf("store.UpsertMerchant: %w", err)
	}
	m.BrandName = shopDomainToBrand(m.ShopDomain)
	return &m, nil
}

// DeactivateMerchant sets active=false and records the uninstall time.
func DeactivateMerchant(ctx context.Context, db *pgxpool.Pool, domain string) error {
	_, err := db.Exec(ctx, `
		UPDATE merchants SET active = false, uninstalled_at = now(), updated_at = now()
		WHERE shop_domain = $1
	`, domain)
	if err != nil {
		return fmt.Errorf("store.DeactivateMerchant: %w", err)
	}
	return nil
}

// ResetMerchantData clears all scan history, fixes, and settings for a merchant
// but keeps the merchant row and Shopify connection intact.
// Used by the merchant-initiated "Delete all my data" action in Settings.
func ResetMerchantData(ctx context.Context, db *pgxpool.Pool, merchantID int64) error {
	tables := []string{
		"citation_records",
		"citation_verifications",
		"pending_fixes",
		"spot_checks",
		"accuracy_metrics",
		"response_stability",
		"scan_costs",
		"visibility_scores",
		"products",
		"merchant_faqs",
	}
	for _, t := range tables {
		if _, err := db.Exec(ctx, `DELETE FROM `+t+` WHERE merchant_id = $1`, merchantID); err != nil {
			return fmt.Errorf("reset merchant data: clear %s: %w", t, err)
		}
	}
	// Reset editable merchant fields but keep the connection.
	_, err := db.Exec(ctx, `
		UPDATE merchants SET
			brand_name              = NULL,
			category                = NULL,
			social_links            = '{}',
			review_app              = NULL,
			review_app_key          = NULL,
			avg_rating              = NULL,
			total_reviews           = 0,
			review_schema_injected  = false,
			reviews_last_scanned_at = NULL,
			updated_at              = NOW()
		WHERE id = $1
	`, merchantID)
	return err
}

// DeleteMerchantData removes all data associated with a shop domain (GDPR).
func DeleteMerchantData(ctx context.Context, db *pgxpool.Pool, domain string) error {
	_, err := db.Exec(ctx, `
		DELETE FROM merchants WHERE shop_domain = $1
	`, domain)
	return err
}

// shopDomainToBrand converts "oakwood-leather.myshopify.com" → "Oakwood Leather".
func shopDomainToBrand(domain string) string {
	// Strip ".myshopify.com" suffix
	name := domain
	for _, suffix := range []string{".myshopify.com", ".shopify.com"} {
		if len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix {
			name = name[:len(name)-len(suffix)]
			break
		}
	}
	// Replace hyphens with spaces and title-case
	result := []byte(name)
	capitalizeNext := true
	for i, b := range result {
		if b == '-' {
			result[i] = ' '
			capitalizeNext = true
		} else if capitalizeNext {
			if b >= 'a' && b <= 'z' {
				result[i] = b - 32
			}
			capitalizeNext = false
		}
	}
	return string(result)
}
