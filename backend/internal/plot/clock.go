package plot

import "fmt"

// ClockMinute reads the game clock into minutes since the first day began, so a
// node's time can be compared with the current one.
func ClockMinute(clock string) (int, error) {
	var day, hour, minute int
	if n, err := fmt.Sscanf(clock, "第 %d 日 %d:%d", &day, &hour, &minute); err != nil || n != 3 || day < 1 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("%w: invalid world clock %q", ErrInvalidProgress, clock)
	}
	return (day-1)*1440 + hour*60 + minute, nil
}
