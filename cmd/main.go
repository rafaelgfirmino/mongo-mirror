package main

import (
	"fmt"
	"log"
	"os"

	"github.com/educbank/mongo-mirror"
	"github.com/urfave/cli/v2"
	"gopkg.in/yaml.v2"
)

func main() {
	app := &cli.App{
		EnableBashCompletion: true,
		Commands: []*cli.Command{
			{
				Name:    "import",
				Aliases: []string{"i"},
				Usage:   "Import data from source to destiny",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "file",
						Aliases:  []string{"f"},
						Usage:    "Path to the YAML file",
						Required: true,
					},
				},
				Action: func(cCtx *cli.Context) error {
					filePath := cCtx.String("file")
					fmt.Printf("Config File: %s", filePath)
					var mirror mongoSync.Mirror
					data, err := os.ReadFile(filePath)
					if err != nil {
						log.Fatal(err)
					}
					err = yaml.Unmarshal(data, &mirror)
					if err != nil {
						log.Fatal(err)
					}
					mirror.LoadConfig()
					mirror.LoadCollections()
					return nil
				},
			},
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
