package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	v1 "github.com/user/firmware-updater/apis/hardware.fabrica.dev/v1"
	"github.com/user/firmware-updater/internal/storage"
)

func newImportCommand() *cobra.Command {
	var (
		input        string
		mode         string
		dryRun       bool
		skipExisting bool
	)

	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import resources from files",
		Long: `Import resources from JSON or YAML files into storage.

This is useful for:
  - Restoring from backups
  - Migrating data between environments
  - Bulk loading resource definitions
  - Testing with known resource state

Import modes:
  - upsert: Create new resources or update existing (default)
  - replace: Delete all resources first, then import
  - skip: Skip resources that already exist

Examples:
  # Import from backup directory
  firmware_updater import --input ./backup

  # Dry run to preview changes
  firmware_updater import --input ./backup --dry-run

  # Replace all resources
  firmware_updater import --input ./backup --mode replace
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runImport(cmd.Context(), input, mode, dryRun, skipExisting)
		},
	}

	cmd.Flags().StringVar(&input, "input", "./backup", "Input directory containing resource files")
	cmd.Flags().StringVar(&mode, "mode", "upsert", "Import mode: upsert, replace, skip")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview changes without applying")
	cmd.Flags().BoolVar(&skipExisting, "skip-existing", false, "Skip resources that already exist (same as --mode skip)")

	return cmd
}

func runImport(ctx context.Context, input, mode string, dryRun, skipExisting bool) error {
	fmt.Printf("🚀 Importing resources...\n")
	fmt.Printf("   Input: %s\n", input)
	fmt.Printf("   Mode: %s\n", mode)
	if dryRun {
		fmt.Printf("   ⚠️  DRY RUN - No changes will be applied\n")
	}

	// Validate mode
	if skipExisting {
		mode = "skip"
	}
	if mode != "upsert" && mode != "replace" && mode != "skip" {
		return fmt.Errorf("unsupported mode: %s (use 'upsert', 'replace', or 'skip')", mode)
	}

	// Check input directory exists
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("input directory does not exist: %w", err)
	}

	// Handle replace mode - delete all resources first
	if mode == "replace" && !dryRun {
		fmt.Printf("⚠️  Replace mode - deleting existing resources...\n")
		if err := deleteAllResources(ctx); err != nil {
			return fmt.Errorf("failed to delete existing resources: %w", err)
		}
	}

	// Walk input directory and import files
	totalImported := 0
	totalSkipped := 0
	var importErr error

	err := filepath.Walk(input, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Only process JSON and YAML files
		ext := filepath.Ext(path)
		if ext != ".json" && ext != ".yaml" && ext != ".yml" {
			return nil
		}

		imported, skipped, err := importFile(ctx, path, mode, dryRun)
		if err != nil {
			fmt.Printf("  ✗ %s: %v\n", filepath.Base(path), err)
			importErr = err
			return nil // Continue with other files
		}
		totalImported += imported
		totalSkipped += skipped
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk import directory: %w", err)
	}

	if dryRun {
		fmt.Printf("✅ Dry run complete. Would import %d resources (%d skipped).\n", totalImported, totalSkipped)
	} else {
		fmt.Printf("✅ Import complete. Imported %d resources (%d skipped).\n", totalImported, totalSkipped)
	}

	return importErr
}

func importFile(ctx context.Context, path string, mode string, dryRun bool) (imported, skipped int, err error) {
	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to read file: %w", err)
	}

	// Determine format
	ext := filepath.Ext(path)

	// Try to unmarshal into generic resource first to determine kind
	var genericResource struct {
		APIVersion string `json:"apiVersion" yaml:"apiVersion"`
		Kind       string `json:"kind" yaml:"kind"`
	}

	if ext == ".json" {
		if err := json.Unmarshal(data, &genericResource); err != nil {
			return 0, 0, fmt.Errorf("failed to parse JSON: %w", err)
		}
	} else {
		if err := yaml.Unmarshal(data, &genericResource); err != nil {
			return 0, 0, fmt.Errorf("failed to parse YAML: %w", err)
		}
	}

	// Import based on kind
	switch genericResource.Kind {
	case "FirmwareUpdateCampaign":
		var res *v1.FirmwareUpdateCampaign
		if ext == ".json" {
			if err := json.Unmarshal(data, &res); err != nil {
				return 0, 0, fmt.Errorf("failed to unmarshal FirmwareUpdateCampaign: %w", err)
			}
		} else {
			if err := yaml.Unmarshal(data, &res); err != nil {
				return 0, 0, fmt.Errorf("failed to unmarshal FirmwareUpdateCampaign: %w", err)
			}
		}

		// Check if resource exists
		existing, err := storage.GetFirmwareUpdateCampaignByUID(ctx, res.Metadata.UID)
		if err == nil && existing != nil {
			// Resource exists
			if mode == "skip" {
				fmt.Printf("  ⊘ %s (exists)\n", filepath.Base(path))
				return 0, 1, nil
			}
			fmt.Printf("  ⟳ %s (updating)\n", filepath.Base(path))
		} else {
			fmt.Printf("  ✓ %s (creating)\n", filepath.Base(path))
		}

		if !dryRun {
			if err := storage.SaveFirmwareUpdateCampaign(ctx, res); err != nil {
				return 0, 0, fmt.Errorf("failed to save FirmwareUpdateCampaign: %w", err)
			}
		}
		return 1, 0, nil
	case "FirmwareUpdateJob":
		var res *v1.FirmwareUpdateJob
		if ext == ".json" {
			if err := json.Unmarshal(data, &res); err != nil {
				return 0, 0, fmt.Errorf("failed to unmarshal FirmwareUpdateJob: %w", err)
			}
		} else {
			if err := yaml.Unmarshal(data, &res); err != nil {
				return 0, 0, fmt.Errorf("failed to unmarshal FirmwareUpdateJob: %w", err)
			}
		}

		// Check if resource exists
		existing, err := storage.GetFirmwareUpdateJobByUID(ctx, res.Metadata.UID)
		if err == nil && existing != nil {
			// Resource exists
			if mode == "skip" {
				fmt.Printf("  ⊘ %s (exists)\n", filepath.Base(path))
				return 0, 1, nil
			}
			fmt.Printf("  ⟳ %s (updating)\n", filepath.Base(path))
		} else {
			fmt.Printf("  ✓ %s (creating)\n", filepath.Base(path))
		}

		if !dryRun {
			if err := storage.SaveFirmwareUpdateJob(ctx, res); err != nil {
				return 0, 0, fmt.Errorf("failed to save FirmwareUpdateJob: %w", err)
			}
		}
		return 1, 0, nil
	default:
		return 0, 0, fmt.Errorf("unknown resource kind: %s", genericResource.Kind)
	}
}

func deleteAllResources(ctx context.Context) error {
	// Delete all firmwareupdatecampaigns
	firmwareupdatecampaignItems, err := storage.Queryfirmwareupdatecampaigns(ctx).All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query firmwareupdatecampaigns: %w", err)
	}
	for _, item := range firmwareupdatecampaignItems {
		if err := storage.DeleteFirmwareUpdateCampaign(ctx, item.UID); err != nil {
			return fmt.Errorf("failed to delete FirmwareUpdateCampaign: %w", err)
		}
	}
	// Delete all firmwareupdatejobs
	firmwareupdatejobItems, err := storage.Queryfirmwareupdatejobs(ctx).All(ctx)
	if err != nil {
		return fmt.Errorf("failed to query firmwareupdatejobs: %w", err)
	}
	for _, item := range firmwareupdatejobItems {
		if err := storage.DeleteFirmwareUpdateJob(ctx, item.UID); err != nil {
			return fmt.Errorf("failed to delete FirmwareUpdateJob: %w", err)
		}
	}
	return nil
}
