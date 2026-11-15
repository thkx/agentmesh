package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"
)

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)

	return encoder.Encode(value)
}

func printTable(headers []string, rows [][]string) {
	writer := tabwriter.NewWriter(
		os.Stdout,
		0,
		4,
		2,
		' ',
		0,
	)

	for i, header := range headers {
		if i > 0 {
			fmt.Fprint(writer, "\t")
		}
		fmt.Fprint(writer, header)
	}
	fmt.Fprintln(writer)

	for _, row := range rows {
		for i, value := range row {
			if i > 0 {
				fmt.Fprint(writer, "\t")
			}
			fmt.Fprint(writer, value)
		}
		fmt.Fprintln(writer)
	}

	_ = writer.Flush()
}

func printMessage(message string) {
	fmt.Println(message)
}

type jsonList[T any] struct {
	values []T
}

func (r *jsonList[T]) UnmarshalJSON(data []byte) error {
	var direct []T

	if len(data) > 0 && data[0] == '[' {
		if err := json.Unmarshal(data, &direct); err != nil {
			return err
		}

		r.values = direct
		return nil
	}

	var envelope struct {
		Data  []T `json:"data"`
		Items []T `json:"items"`
	}

	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}

	if envelope.Data != nil {
		r.values = envelope.Data
	} else {
		r.values = envelope.Items
	}

	return nil
}

func (r jsonList[T]) Items() []T {
	return r.values
}
