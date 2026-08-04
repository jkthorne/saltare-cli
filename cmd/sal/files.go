package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jkthorne/saltare/cli/internal/api"
	"github.com/jkthorne/saltare/cli/internal/config"
	"github.com/jkthorne/saltare/cli/internal/tablefmt"
)

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func runFiles(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "put":
			return filesPut(args[1:])
		case "get":
			return filesGet(args[1:])
		case "rm":
			if len(args) != 2 {
				return fmt.Errorf("usage: sal files rm SLUG")
			}
			return filesRm(args[1])
		}
	}

	fs := flag.NewFlagSet("files", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "print raw JSON")
	category := fs.String("category", "", "filter by category (image, pdf, text, …)")
	query := fs.String("q", "", "full-text over title, filename, and extracted text")
	if err := fs.Parse(args); err != nil {
		return err
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	uploads, err := client.Uploads(ctx, api.UploadsOpts{Category: *category, Query: *query})
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(uploads)
	}
	for _, u := range uploads {
		category := "pending"
		if u.Category != nil {
			category = *u.Category
		}
		fmt.Printf("%-24s %-12s %8s  %s\n", u.Slug, category, tablefmt.HumanSize(u.FileSize), u.Title)
	}
	return nil
}

func filesPut(args []string) error {
	fs := flag.NewFlagSet("files put", flag.ExitOnError)
	title := fs.String("title", "", "display title (defaults to the filename)")
	path, err := slugAndFlags(fs, args, "sal files put PATH [--title T]")
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory — sal files put takes one file", path)
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	upload, err := client.UploadFile(ctx, path, *title)
	if err != nil {
		return err
	}
	fmt.Printf("uploaded %s (%s) — embed with [[upload:%s]]\n", upload.Slug, tablefmt.HumanSize(upload.FileSize), upload.Slug)
	fmt.Fprintln(os.Stderr, "· category is computed in the background; sal files will show it shortly")
	return nil
}

func filesGet(args []string) error {
	fs := flag.NewFlagSet("files get", flag.ExitOnError)
	out := fs.String("o", "", "write to this path (defaults to the original filename)")
	force := fs.Bool("force", false, "overwrite an existing file")
	slug, err := slugAndFlags(fs, args, "sal files get SLUG [-o PATH] [--force]")
	if err != nil {
		return err
	}

	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	record, err := client.UploadRecord(ctx, slug)
	if err != nil {
		return err
	}

	target := *out
	if target == "" {
		target = record.OriginalFilename
	}
	if target == "" {
		target = slug
	}
	if _, err := os.Stat(target); err == nil && !*force {
		return fmt.Errorf("%s already exists — pass --force to overwrite", target)
	}

	body, _, _, err := client.DownloadUpload(ctx, slug)
	if err != nil {
		return err
	}
	defer body.Close()

	written, err := config.SafeWriteFile(target, body)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (%s)\n", target, tablefmt.HumanSize(written))
	return nil
}

func filesRm(slug string) error {
	_, client, err := session()
	if err != nil {
		return err
	}
	ctx, cancel := interruptContext()
	defer cancel()

	if err := client.DeleteUpload(ctx, slug); err != nil {
		return err
	}
	fmt.Printf("deleted %s\n", slug)
	return nil
}
