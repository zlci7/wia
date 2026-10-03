package plot

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

const DateClockLayout = "2006-01-02 15:04"

var relativeClock = regexp.MustCompile(`^第 ([1-9][0-9]*) 日 ([0-9]{1,2}):([0-9]{2})$`)
var minuteEpoch = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC).Unix() / 60

func dateClock(clock string) (time.Time, bool) {
	value, err := time.ParseInLocation(DateClockLayout, clock, time.UTC)
	return value, err == nil && value.Year() >= 1 && value.Year() <= 9999 && value.Format(DateClockLayout) == clock
}

// Authored dates use Gregorian minutes from year 1. Frozen relative clocks keep
// their original epoch; neither representation uses the host's local time.
func ClockMinute(clock string) (int, error) {
	if value, ok := dateClock(clock); ok {
		minute := value.Unix()/60 - minuteEpoch
		if minute > int64(math.MaxInt) {
			return 0, fmt.Errorf("%w: world clock exceeds integer range", ErrInvalidProgress)
		}
		return int(minute), nil
	}
	parts := relativeClock.FindStringSubmatch(clock)
	if len(parts) != 4 {
		return 0, fmt.Errorf("%w: invalid world clock", ErrInvalidProgress)
	}
	day, err := strconv.Atoi(parts[1])
	hour, _ := strconv.Atoi(parts[2])
	minute, _ := strconv.Atoi(parts[3])
	if err != nil || day > (math.MaxInt-1439)/1440 || hour > 23 || minute > 59 {
		return 0, fmt.Errorf("%w: invalid world clock", ErrInvalidProgress)
	}
	return (day-1)*1440 + hour*60 + minute, nil
}

// ClockDayStart supplies the compilation epoch for authored minute schedules.
func ClockDayStart(clock string) (int, error) {
	minute, err := ClockMinute(clock)
	if err != nil {
		return 0, err
	}
	if !IsDateClock(clock) {
		return 0, nil
	}
	return minute - minute%1440, nil
}

func IsDateClock(clock string) bool { _, ok := dateClock(clock); return ok }

func AdvanceClock(clock string, minutes int) (string, error) {
	current, err := ClockMinute(clock)
	if err != nil || minutes < 0 || minutes > math.MaxInt-current {
		return "", fmt.Errorf("%w: invalid clock advance", ErrInvalidProgress)
	}
	if value, ok := dateClock(clock); ok {
		maxMinute, _ := ClockMinute("9999-12-31 23:59")
		if minutes > maxMinute-current {
			return "", fmt.Errorf("%w: world date outside supported years", ErrInvalidProgress)
		}
		return time.Unix(value.Unix()+int64(minutes)*60, 0).UTC().Format(DateClockLayout), nil
	}
	total := current + minutes
	return fmt.Sprintf("第 %d 日 %02d:%02d", total/1440+1, total/60%24, total%60), nil
}
