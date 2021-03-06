/*****************************************************************************
 **
 ** Gofr
 ** https://github.com/pokebyte/Gofr
 ** Copyright (C) 2013-2017 Akop Karapetyan
 **
 ** This program is free software; you can redistribute it and/or modify
 ** it under the terms of the GNU General Public License as published by
 ** the Free Software Foundation; either version 2 of the License, or
 ** (at your option) any later version.
 **
 ** This program is distributed in the hope that it will be useful,
 ** but WITHOUT ANY WARRANTY; without even the implied warranty of
 ** MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 ** GNU General Public License for more details.
 **
 ** You should have received a copy of the GNU General Public License
 ** along with this program; if not, write to the Free Software
 ** Foundation, Inc., 675 Mass Ave, Cambridge, MA 02139, USA.
 **
 ******************************************************************************
 */
 
package rss

import (
	"encoding/xml"
	"errors"
 	"sort"
	"strings"
	"time"
)

type (
	rss2Feed struct {
		XMLName xml.Name `xml:"rss"`
		Title string `xml:"channel>title"`
		Description string `xml:"channel>description"`
		Updated string `xml:"channel>lastBuildDate"`
		Link []*rssLink `xml:"channel>link"`
		Entry []*rss2Entry `xml:"channel>item"`
		UpdatePeriod string `xml:"channel>updatePeriod"`
		UpdateFrequency int `xml:"channel>updateFrequency"`
	}
	rss2Entry struct {
		Id string `xml:"guid"`
		Published string `xml:"pubDate"`
		EntryTitle string `xml:"title"`
		Link string `xml:"link"`
		Author string `xml:"creator"`
		EncodedContent string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
		Content string `xml:"description"`
		Enclosures []rss2Enclosure `xml:"enclosure"`
	}
	rss2Enclosure struct {
		URL string `xml:"url,attr"`
		Length int `xml:"length,attr"`
		Type string `xml:"type,attr"`
	}
	timezone struct {
		Code string
		Offset string
	}
	timezoneList []timezone
)

var (
	// Basic TZ map to improve Golang's understanding of timezone shorthands
	tzMap = map[string]string {
		"EEST": "+0300",
		"AKST": "-0900",
		"AKDT": "-0800",
		"HAST": "-1000",
		"HADT": "-0900",
		"CHST": "+1000",
		"EET":  "+0200",
		"AST":  "-0400",
		"EST":  "-0500",
		"EDT":  "-0400",
		"CST":  "-0600",
		"CDT":  "-0500",
		"MST":  "-0700",
		"MDT":  "-0600",
		"PST":  "-0800",
		"PDT":  "-0700",
		"SST":  "-1100",
		"SDT":  "-1000",
		"CET":  "+0100",
	}
	timezones timezoneList

	supportedRSS2TimeFormats = []string {
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"2006-01-02T15:04:05-07:00",
		"Mon, 02 Jan 2006 15:04:05 Z",
		"Mon, 02 Jan 2006 15:04:05",
		"Mon, 2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04:05",
		"2 Jan 2006 15:04:05 -0700",
		"Mon, 2 Jan 2006 15:04 -0700",
		"Mon, 2 Jan 06 15:04:05 -0700",
		"January 2, 2006",
	}
)

func (s timezoneList) Len() int {
	return len(s)
}

func (s timezoneList) Swap(i int, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s timezoneList) Less(i int, j int) bool {
	// Longer codes before shorter ones
	return len(s[i].Code) > len(s[j].Code)
}

func init() {
	timezones = make(timezoneList, len(tzMap))

	// Put timezones into an array
	i := 0
	for code, offset := range tzMap {
		timezones[i] = timezone {
			Code: code,
			Offset: offset,
		}
		i++
	}

	// Sort the array (longer codes first)
	sort.Sort(timezones)
}

func (nativeFeed *rss2Feed) Marshal() (feed *Feed, err error) {
	updated := time.Time {}
	if nativeFeed.Updated != "" {
		updated, err = parseRSS2Time(nativeFeed.Updated)
	}

	hubURL := ""
	linkUrl := ""
	topic := ""

	for _, link := range nativeFeed.Link {
		if link.XMLName.Space == "" {
			linkUrl = link.Content
		} else if link.XMLName.Space == "http://www.w3.org/2005/Atom" {
			for _, rel := range strings.Split(link.Rel, " ") {
				if rel == "self" {
					topic = link.Href
					break
				} else if rel == "hub" {
					hubURL = link.Href
					break
				}
			}
		}
	}

	feed = &Feed {
		Title: nativeFeed.Title,
		Description: nativeFeed.Description,
		Updated: updated,
		WWWURL: linkUrl,
		Format: "RSS2",
		Topic: topic,
		HubURL: hubURL,
	}

	if nativeFeed.UpdateFrequency != 0 && nativeFeed.UpdatePeriod != "" {
		updateFrequency := nativeFeed.UpdateFrequency
		updatePeriod := strings.ToLower(nativeFeed.UpdatePeriod)

		if updatePeriod == "hourly" {
			feed.HourlyUpdateFrequency = 1.0 / float32(updateFrequency)
		} else if updatePeriod == "weekly" {
			feed.HourlyUpdateFrequency = (24.0 * 7.0) / float32(updateFrequency)
		} else if updatePeriod == "monthly" {
			feed.HourlyUpdateFrequency = (24.0 * 30.42) / float32(updateFrequency)
		} else if updatePeriod == "yearly" {
			feed.HourlyUpdateFrequency = (24.0 * 365.25) / float32(updateFrequency)
		} else { // if updatePeriod == "daily" {
			feed.HourlyUpdateFrequency = 24.0 / float32(updateFrequency)
		}
	}

	if nativeFeed.Entry != nil {
		feed.Entries = make([]*Entry, len(nativeFeed.Entry))
		for i, v := range nativeFeed.Entry {
			var entryError error
			feed.Entries[i], entryError = v.Marshal()

			if entryError != nil && err == nil {
				err = entryError
			}
		}
	}

	return feed, err
}

func (nativeEntry *rss2Entry) Marshal() (entry *Entry, err error) {
	guid := nativeEntry.Id
	content := nativeEntry.EncodedContent
	if content == "" {
		content = nativeEntry.Content
	}

	published := time.Time {}
	if nativeEntry.Published != "" {
		published, err = parseRSS2Time(nativeEntry.Published)
	}

	entry = &Entry {
		GUID: guid,
		Author: nativeEntry.Author,
		Title: nativeEntry.EntryTitle,
		Content: content,
		Published: published,
		WWWURL: nativeEntry.Link,
		Media: make([]Media, len(nativeEntry.Enclosures)),
	}

	for i, enclosure := range nativeEntry.Enclosures {
		media := Media {
			URL: enclosure.URL,
			Type: enclosure.Type,
		}

		entry.Media[i] = media
	}

	return entry, err
}

func parseRSS2Time(timeSpec string) (time.Time, error) {
	if timeSpec != "" {
		if parsedTime, err := parseTime(supportedRSS2TimeFormats, timeSpec); err == nil {
			return parsedTime, err
		}

		// HACK territory
		// GMT/UTC as TZ code are OK
		if strings.HasSuffix(timeSpec, " GMT") || strings.HasSuffix(timeSpec, " UTC") {
			if parsedTime, err := time.Parse("Mon, 2 Jan 2006 15:04:05 MST", timeSpec); err == nil {
				return parsedTime.UTC(), nil
			}
		}

		// FIXME
		// time.Parse doesn't deal with timezone codes predictably. 
		// For that reason, we replace timezone codes with UTC offsets
		// Note that this is not a proper long-term solution

		tryAgain := false
		for _, tz := range timezones {
			if strings.Contains(timeSpec, tz.Code) {
				timeSpec = strings.Replace(timeSpec, tz.Code, tz.Offset, 1)
				tryAgain = true
				break
			}
		}

		if tryAgain {
			if parsedTime, err := parseTime(supportedRSS2TimeFormats, timeSpec); err == nil {
				return parsedTime, err
			}
		}

		return time.Time {}, errors.New("Unrecognized time format: " + timeSpec)
	}

	return time.Time {}, nil
}
