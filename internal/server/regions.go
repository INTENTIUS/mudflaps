package server

import "github.com/intentius/mudflaps/internal/flaps"

// flyRegions is a static, representative slice of Fly.io regions. It exists so a
// client (e.g. a lint rule) can validate a machine's region against a real list
// without a live platform. It is not exhaustive; add codes as needed.
var flyRegions = []flaps.Region{
	{Code: "ams", Name: "Amsterdam, Netherlands", GatewayAvailable: true},
	{Code: "atl", Name: "Atlanta, Georgia (US)"},
	{Code: "bog", Name: "Bogotá, Colombia"},
	{Code: "bos", Name: "Boston, Massachusetts (US)"},
	{Code: "cdg", Name: "Paris, France", GatewayAvailable: true},
	{Code: "den", Name: "Denver, Colorado (US)"},
	{Code: "dfw", Name: "Dallas, Texas (US)", GatewayAvailable: true},
	{Code: "ewr", Name: "Secaucus, NJ (US)"},
	{Code: "fra", Name: "Frankfurt, Germany", GatewayAvailable: true},
	{Code: "gru", Name: "São Paulo, Brazil"},
	{Code: "hkg", Name: "Hong Kong, Hong Kong", GatewayAvailable: true},
	{Code: "iad", Name: "Ashburn, Virginia (US)", GatewayAvailable: true},
	{Code: "jnb", Name: "Johannesburg, South Africa"},
	{Code: "lax", Name: "Los Angeles, California (US)", GatewayAvailable: true},
	{Code: "lhr", Name: "London, United Kingdom", GatewayAvailable: true},
	{Code: "mad", Name: "Madrid, Spain"},
	{Code: "mia", Name: "Miami, Florida (US)"},
	{Code: "nrt", Name: "Tokyo, Japan", GatewayAvailable: true},
	{Code: "ord", Name: "Chicago, Illinois (US)", GatewayAvailable: true},
	{Code: "scl", Name: "Santiago, Chile"},
	{Code: "sea", Name: "Seattle, Washington (US)"},
	{Code: "sin", Name: "Singapore, Singapore", GatewayAvailable: true},
	{Code: "sjc", Name: "San Jose, California (US)", GatewayAvailable: true},
	{Code: "syd", Name: "Sydney, Australia", GatewayAvailable: true},
	{Code: "yyz", Name: "Toronto, Canada", GatewayAvailable: true},
}
