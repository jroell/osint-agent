package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type ShopifyVariant struct {
	ID              int64   `json:"id"`
	Title           string  `json:"title,omitempty"`
	SKU             string  `json:"sku,omitempty"`
	Price           string  `json:"price,omitempty"`
	CompareAtPrice  string  `json:"compare_at_price,omitempty"`
	Available       bool    `json:"available"`
	Grams           int     `json:"grams,omitempty"`
	Option1         string  `json:"option1,omitempty"`
	Option2         string  `json:"option2,omitempty"`
	Option3         string  `json:"option3,omitempty"`
}

type ShopifyProduct struct {
	ID            int64            `json:"id"`
	Title         string           `json:"title"`
	Handle        string           `json:"handle"`
	Vendor        string           `json:"vendor,omitempty"`
	ProductType   string           `json:"product_type,omitempty"`
	Tags          []string         `json:"tags,omitempty"`
	Variants      []ShopifyVariant `json:"variants,omitempty"`
	Images        int              `json:"image_count"`
	Description   string           `json:"description_text,omitempty"`
	PublishedAt   string           `json:"published_at,omitempty"`
	CreatedAt     string           `json:"created_at,omitempty"`
	UpdatedAt     string           `json:"updated_at,omitempty"`
	URL           string           `json:"product_url"`
	PriceMin      string           `json:"price_min,omitempty"`
	PriceMax      string           `json:"price_max,omitempty"`
	OutOfStock    bool             `json:"out_of_stock"`
	HasDiscount   bool             `json:"has_discount"`
}

type ShopifyStorefrontExtractOutput struct {
	Shop              string           `json:"shop_url"`
	IsShopify         bool             `json:"is_shopify"`
	TotalProducts     int              `json:"total_products_returned"`
	Products          []ShopifyProduct `json:"products"`
	UniqueVendors     []string         `json:"unique_vendors"`
	UniqueProductTypes []string        `json:"unique_product_types"`
	OutOfStockCount   int              `json:"out_of_stock_count"`
	DiscountCount     int              `json:"on_sale_count"`
	PricesObserved    []string         `json:"price_range_sample,omitempty"`
	InternalTagPatterns []string       `json:"internal_tag_patterns,omitempty"` // tags with "::" syntax often reveal internal taxonomies
	Source            string           `json:"source"`
	TookMs            int64            `json:"tookMs"`
	Note              string           `json:"note,omitempty"`
}

// ShopifyStorefrontExtract fetches the public /products.json endpoint of a
// Shopify storefront. This is a built-in Shopify feature — every Shopify
// store exposes it, no auth required, no rate limit at reasonable scale.
//
// Returns: complete product catalog with prices, variants, SKUs, vendors,
// product types, tags. Tags with "::" syntax (e.g., "brand::carbon-score => 5.9")
// often reveal INTERNAL TAXONOMIES that the brand never intended to expose.
//
// Use cases:
//   - Competitive intel: full pricing snapshot of a competitor's catalog
//   - Counterfeit detection: compare brand's official catalog to lookalike sites
//   - Supply-chain mapping: vendor names + carbon scores + sustainability tags
//   - Inventory inference: out-of-stock signals reveal popular SKUs
//   - Pricing intelligence: compare_at_price reveals MSRP vs. current sale
//
// Free, no key. Most underexploited OSINT surface in retail/e-commerce.
func ShopifyStorefrontExtract(ctx context.Context, input map[string]any) (*ShopifyStorefrontExtractOutput, error) {
	shop, _ := input["shop"].(string)
	shop = strings.TrimSpace(strings.ToLower(shop))
	if shop == "" {
		return nil, errors.New("input.shop required (e.g. 'allbirds.com' or 'https://www.allbirds.com')")
	}
	// Normalize
	if !strings.HasPrefix(shop, "http://") && !strings.HasPrefix(shop, "https://") {
		shop = "https://" + shop
	}
	shop = strings.TrimRight(shop, "/")

	limit := 50
	if v, ok := input["limit"].(float64); ok && int(v) > 0 && int(v) <= 250 {
		limit = int(v)
	}
	page := 1
	if v, ok := input["page"].(float64); ok && int(v) > 0 {
		page = int(v)
	}

	start := time.Now()
	endpoint := fmt.Sprintf("%s/products.json?limit=%d&page=%d", shop, limit, page)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(cctx, http.MethodGet, endpoint, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; osint-agent/shopify)")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("shop fetch failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))

	out := &ShopifyStorefrontExtractOutput{Shop: shop, Source: "shopify-storefront"}

	if resp.StatusCode == 404 || resp.StatusCode == 403 {
		out.IsShopify = false
		out.Note = fmt.Sprintf("status %d — not a Shopify store, or /products.json is restricted on this storefront.", resp.StatusCode)
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var parsed struct {
		Products []struct {
			ID          int64    `json:"id"`
			Title       string   `json:"title"`
			Handle      string   `json:"handle"`
			BodyHTML    string   `json:"body_html"`
			Vendor      string   `json:"vendor"`
			ProductType string   `json:"product_type"`
			Tags        []string `json:"tags"`
			PublishedAt string   `json:"published_at"`
			CreatedAt   string   `json:"created_at"`
			UpdatedAt   string   `json:"updated_at"`
			Images      []any    `json:"images"`
			Variants    []struct {
				ID             int64   `json:"id"`
				Title          string  `json:"title"`
				SKU            string  `json:"sku"`
				Price          string  `json:"price"`
				CompareAtPrice string  `json:"compare_at_price"`
				Available      bool    `json:"available"`
				Grams          int     `json:"grams"`
				Option1        string  `json:"option1"`
				Option2        string  `json:"option2"`
				Option3        string  `json:"option3"`
			} `json:"variants"`
		} `json:"products"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		out.IsShopify = false
		out.Note = "Response is not Shopify-shaped JSON — likely not a Shopify store"
		out.TookMs = time.Since(start).Milliseconds()
		return out, nil
	}

	out.IsShopify = true
	vendorSet := map[string]bool{}
	productTypeSet := map[string]bool{}
	internalTagSet := map[string]bool{}
	priceSamples := map[string]bool{}

	for _, p := range parsed.Products {
		desc := stripHTMLBare(p.BodyHTML)
		if len(desc) > 200 {
			desc = desc[:200] + "…"
		}
		variants := []ShopifyVariant{}
		anyAvailable := false
		anyDiscount := false
		minPrice, maxPrice := "", ""
		for _, v := range p.Variants {
			variants = append(variants, ShopifyVariant{
				ID: v.ID, Title: v.Title, SKU: v.SKU, Price: v.Price,
				CompareAtPrice: v.CompareAtPrice, Available: v.Available, Grams: v.Grams,
				Option1: v.Option1, Option2: v.Option2, Option3: v.Option3,
			})
			if v.Available {
				anyAvailable = true
			}
			if v.CompareAtPrice != "" && v.CompareAtPrice != v.Price && v.CompareAtPrice > v.Price {
				anyDiscount = true
			}
			if v.Price != "" {
				priceSamples[v.Price] = true
				if minPrice == "" || v.Price < minPrice {
					minPrice = v.Price
				}
				if v.Price > maxPrice {
					maxPrice = v.Price
				}
			}
		}
		// Internal tag patterns (vendor::key => value)
		for _, t := range p.Tags {
			if strings.Contains(t, "::") || strings.Contains(t, "=>") {
				internalTagSet[t] = true
			}
		}
		out.Products = append(out.Products, ShopifyProduct{
			ID: p.ID, Title: p.Title, Handle: p.Handle,
			Vendor: p.Vendor, ProductType: p.ProductType, Tags: p.Tags,
			Variants: variants, Images: len(p.Images),
			Description: desc, PublishedAt: p.PublishedAt,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
			URL:       fmt.Sprintf("%s/products/%s", shop, url.PathEscape(p.Handle)),
			PriceMin:  minPrice, PriceMax: maxPrice,
			OutOfStock: !anyAvailable, HasDiscount: anyDiscount,
		})
		if p.Vendor != "" {
			vendorSet[p.Vendor] = true
		}
		if p.ProductType != "" {
			productTypeSet[p.ProductType] = true
		}
		if !anyAvailable {
			out.OutOfStockCount++
		}
		if anyDiscount {
			out.DiscountCount++
		}
	}

	out.TotalProducts = len(out.Products)
	for v := range vendorSet {
		out.UniqueVendors = append(out.UniqueVendors, v)
	}
	for pt := range productTypeSet {
		out.UniqueProductTypes = append(out.UniqueProductTypes, pt)
	}
	for t := range internalTagSet {
		out.InternalTagPatterns = append(out.InternalTagPatterns, t)
	}
	for p := range priceSamples {
		out.PricesObserved = append(out.PricesObserved, p)
	}
	sort.Strings(out.UniqueVendors)
	sort.Strings(out.UniqueProductTypes)
	sort.Strings(out.InternalTagPatterns)
	sort.Strings(out.PricesObserved)
	if len(out.PricesObserved) > 30 {
		out.PricesObserved = out.PricesObserved[:30]
	}
	if len(out.InternalTagPatterns) > 30 {
		out.InternalTagPatterns = out.InternalTagPatterns[:30]
	}

	out.TookMs = time.Since(start).Milliseconds()
	return out, nil
}
