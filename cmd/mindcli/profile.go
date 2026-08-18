package main

import (
	"fmt"
	"os"

	"github.com/J-1000/mindcli/internal/config"
)

func runProfile(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mindcli profile <create|list> [name]")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("usage: mindcli profile list")
		}
		if os.Getenv("MINDCLI_CONFIG_PATH") != "" {
			fmt.Printf("* %s (exact config override)\n", activeProfile)
			return nil
		}
		profiles, err := config.ListProfiles()
		if err != nil {
			return fmt.Errorf("listing profiles: %w", err)
		}
		for _, profile := range profiles {
			marker := " "
			if profile == activeProfile {
				marker = "*"
			}
			fmt.Printf("%s %s\n", marker, profile)
		}
		return nil

	case "create":
		if len(args) != 2 {
			return fmt.Errorf("usage: mindcli profile create <name>")
		}
		if os.Getenv("MINDCLI_CONFIG_PATH") != "" {
			return fmt.Errorf("cannot create an isolated profile while MINDCLI_CONFIG_PATH is set")
		}
		name, err := config.ValidateProfileName(args[1])
		if err != nil {
			return err
		}
		path, err := config.ConfigPathForProfile(name)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("profile %q already exists at %s", name, path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("checking profile config: %w", err)
		}
		cfg, err := config.DefaultForProfile(name)
		if err != nil {
			return err
		}
		if err := cfg.SaveProfile(name); err != nil {
			return fmt.Errorf("creating profile: %w", err)
		}
		fmt.Printf("Created profile %q\nConfig: %s\nData: %s\nInbox: %s\n", name, path, cfg.Storage.Path, cfg.Capture.Inbox)
		return nil

	default:
		return fmt.Errorf("unknown profile subcommand %q: use create or list", args[0])
	}
}
