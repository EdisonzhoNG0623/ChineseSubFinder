package subdl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

type apiResponse struct {
	Status    bool           `json:"status"`
	Message   string         `json:"message"`
	Results   []mediaResult  `json:"results"`
	Subtitles []subtitleItem `json:"subtitles"`
}

type mediaResult struct {
	SDID   stringValue `json:"sd_id"`
	Name   string      `json:"name"`
	Type   string      `json:"type"`
	IMDbID stringValue `json:"imdb_id"`
	TMDBID stringValue `json:"tmdb_id"`
	Year   intValue    `json:"year"`
}

type subtitleItem struct {
	ID          stringValue  `json:"id"`
	ReleaseName string       `json:"release_name"`
	Name        string       `json:"name"`
	Language    string       `json:"language"`
	Lang        string       `json:"lang"`
	URL         string       `json:"url"`
	Season      intValue     `json:"season"`
	Episode     intValue     `json:"episode"`
	FullSeason  boolValue    `json:"full_season"`
	UnpackFiles []unpackFile `json:"unpack_files"`
}

type unpackFile struct {
	Name          string   `json:"name"`
	SeasonNumber  intValue `json:"season_number"`
	EpisodeNumber intValue `json:"episode_number"`
}

type stringValue string

func (v *stringValue) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*v = ""
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		*v = stringValue(text)
		return nil
	}
	var number json.Number
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*v = stringValue(number.String())
	return nil
}

type intValue int

func (v *intValue) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) || bytes.Equal(data, []byte(`""`)) {
		*v = 0
		return nil
	}
	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		*v = intValue(number)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := strconv.Atoi(text)
	if err != nil {
		return fmt.Errorf("parse integer %q: %w", text, err)
	}
	*v = intValue(parsed)
	return nil
}

type boolValue bool

func (v *boolValue) UnmarshalJSON(data []byte) error {
	var value bool
	if err := json.Unmarshal(data, &value); err == nil {
		*v = boolValue(value)
		return nil
	}
	var number int
	if err := json.Unmarshal(data, &number); err == nil {
		*v = boolValue(number != 0)
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := strconv.ParseBool(text)
	if err != nil {
		return fmt.Errorf("parse boolean %q: %w", text, err)
	}
	*v = boolValue(parsed)
	return nil
}
