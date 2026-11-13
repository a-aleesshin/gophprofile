package clock

import "time"

type System struct{}

func (System) Now() time.Time { return time.Now().UTC() }

type Fixed struct {
	At time.Time
}

func (f Fixed) Now() time.Time { return f.At }
