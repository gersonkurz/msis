// Package cabinet extracts a Microsoft cabinet held in memory.
//
// It is shared: an MSI carries its payload in one (internal/msiread) and a Burn bundle carries
// both its bootstrapper application and its chained packages in others (internal/burnread).
// Splitting it out when the second consumer arrived is the point at which duplication would
// otherwise have started.
package cabinet
