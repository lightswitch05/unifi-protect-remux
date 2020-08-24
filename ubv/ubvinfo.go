package ubv

import (
	"bufio"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	UBVINFO_INSTALL_LOCATION = "/usr/share/unifi-protect/app/node_modules/.bin/ubnt_ubvinfo"
)

// Analyse a .ubv file
func Analyse(ubvFile string) UbvFile {
	cachedUbvInfoFile := ubvFile + ".txt"

	if _, err := os.Stat(cachedUbvInfoFile); err != nil {
		// No existing analysis, must run ubnt_ubvinfo
		return runUbvInfo(ubvFile)
	} else {
		// Analysis file exists, read that instead of re-running ubnt_ubvinfo
		return parseUbvInfoFile(ubvFile, cachedUbvInfoFile)
	}
}

func runUbvInfo(ubvFile string) UbvFile {
	var cmd string

	if _, err := exec.LookPath("ubnt_ubvinfo"); err != nil {
		// ubnt_ubvinfo not in the user's PATH; check if it's in the Ubiquiti shipped path

		if _, err := os.Stat(UBVINFO_INSTALL_LOCATION); err != nil {
			log.Fatal("ubnt_ubvinfo not on PATH, nor in default location ", UBVINFO_INSTALL_LOCATION)
		} else {
			cmd = UBVINFO_INSTALL_LOCATION
		}
	} else {
		cmd = "ubnt_ubvinfo"
	}

	return runUbvInfoCommand(cmd, ubvFile)
}

func runUbvInfoCommand(commandPath string, ubvFile string) UbvFile {
	cmd := exec.Command(commandPath, "-P", "-f", ubvFile)

	cmdReader, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatal("Error creating StdoutPipe for Cmd: ", err)
	}

	scanner := bufio.NewScanner(cmdReader)

	// Parse stdout in the background
	var info UbvFile
	go func() {
		info = parseUbvInfo(ubvFile, scanner)
	}()

	err = cmd.Start()
	if err != nil {
		log.Fatal("ubnt_ubvinfo command failed against ", ubvFile, ": ", err)
	}

	// Await the parsed UBV Info
	for !info.Complete {
		time.Sleep(100 * time.Millisecond)
	}

	// Call wait so stdout/stderr pipes are cleaned up
	err = cmd.Wait()
	if err != nil {
		log.Fatal("Error waiting for ubv: ", err)
	}

	return info
}

func parseUbvInfoFile(ubvFile string, ubvInfoFile string) UbvFile {
	f, err := os.Open(ubvInfoFile)

	if err != nil {
		log.Fatal(err)
	}

	defer f.Close()

	scanner := bufio.NewScanner(f)

	return parseUbvInfo(ubvFile, scanner)
}

func parseUbvInfo(ubvFile string, scanner *bufio.Scanner) UbvFile {
	var err error

	var firstLine bool
	var partitions []*UbvPartition
	var current *UbvPartition

	firstLine = true

	for scanner.Scan() {
		line := scanner.Text()

		if firstLine {
			firstLine = false
		} else if line == "----------- PARTITION START -----------" {
			log.Printf("New partition")
			// Start a new partition
			current = &UbvPartition{
				Index:  len(partitions),
				Tracks: make(map[int]*UbvTrack),
			}

			partitions = append(partitions, current)

		} else if len(line) != 0 && unicode.IsSpace([]rune(line)[0]) {
			// Line starts with whitespace, is a frame

			fields := strings.Fields(line)

			var frame = UbvFrame{}

			if frame.TrackNumber, err = strconv.Atoi(fields[FIELD_TRACK_ID]); err != nil {
				log.Fatal("Error parsing track number!", err)
			}
			if frame.Offset, err = strconv.Atoi(fields[FIELD_OFFSET]); err != nil {
				log.Fatal("Error parsing field offset!", err)
			}
			if frame.Size, err = strconv.Atoi(fields[FIELD_SIZE]); err != nil {
				log.Fatal("Error parsing frame size!", err)
			}

			// Bail if we encounter an unexpected track number
			// We could silently ignore it, but it seems more useful to know about new cases
			if frame.TrackNumber != 7 && frame.TrackNumber != 1000 {
				log.Fatal("Encountered track number other than 7 or 1000: ", frame.TrackNumber)
			}

			track, ok := current.Tracks[frame.TrackNumber]

			if !ok {
				track = &UbvTrack{
					// TODO should really test field FIELD_TRACK_TYPE holds (A or V)
					IsVideo:     frame.TrackNumber == 7,
					TrackNumber: frame.TrackNumber,
					FrameCount:  0,
				}

				current.Tracks[frame.TrackNumber] = track

				if track.IsVideo {
					current.VideoTrackCount++
				} else {
					current.AudioTrackCount++
				}
			}

			// Add Timecode and Rate data to the Track record
			extractTimecodeAndRate(fields, line, track)

			current.FrameCount++
			track.FrameCount++
			current.Frames = append(current.Frames, frame)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal("error reading ubv", ubvFile, err)
	}

	return UbvFile{
		Complete:   true,
		Filename:   ubvFile,
		Partitions: partitions,
	}
}
