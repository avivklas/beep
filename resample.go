package beep

import "fmt"

// Resample takes a Streamer which is assumed to stream at the old sample rate and returns a
// Streamer, which streams the data from the original Streamer, but in the new sample rate.
//
// This is, for example, useful when mixing multiple Streamer with different sample rates, either
// through a beep.Mixer, or through a speaker. Speaker has a constant sample rate. Thus, playing
// Streamer which stream at a different sample rate will lead to a changed speed and pitch of the
// playback.
//
//   sr := beep.SampleRate(48000) speaker.Init(sr, sr.N(time.Second/2))
//   speaker.Play(beep.Resample(3, format.SampleRate, sr, s))
//
// In the example, the original sample rate of the source if format.SampleRate. We want to play it
// at the speaker's native sample rate and thus we need to resample.
//
// The quality argument specifies the quality of the resampling process. Higher quality implies
// worse performance. Values below 0 or above 64 are invalid and Resample will panic. Here's a table
// for deciding which quality to pick.
//
//   quality | use case
//   --------|---------
//   1       | very high performance, on-the-fly resampling, low quality
//   3-4     | good performance, on-the-fly resampling, good quality,
//   6       | higher CPU usage, usually not suitable for on-the-fly resampling, very good quality
//   >6      | even higher CPU usage, for offline resampling, very good quality
//
// Sane quality values are usually below 16. Higher values will consume too much CPU, giving
// negligible quality improvements.
func Resample(quality int, old, new SampleRate, s Streamer) Streamer {
	if quality < 1 || 64 < quality {
		panic(fmt.Errorf("resample: invalid quality: %d", quality))
	}
	return &resample{
		s:     s,
		ratio: float64(old) / float64(new),
		first: true,
		buf1:  make([][2]float64, 512),
		buf2:  make([][2]float64, 512),
		pts:   make([]point, quality*2),
		off:   0,
		pos:   0,
	}
}

type resample struct {
	s          Streamer     // the orignal streamer
	ratio      float64      // old sample rate / new sample rate
	first      bool         // true when Stream was not called before
	buf1, buf2 [][2]float64 // buf1 contains previous buf2, new data goes into buf2, buf1 is because interpolation might require old samples
	pts        []point      // pts is for points used for interpolation
	off        int          // off is the position of the start of buf2 in the original data
	pos        int          // pos is the current position in the resampled data
}

func (r *resample) Stream(samples [][2]float64) (n int, ok bool) {
	if r.first { // if it's the first time, we need to fill buf2 with initial data, buf1 remains zeroed
		sn, _ := r.s.Stream(r.buf2)
		r.buf2 = r.buf2[:sn]
		r.first = false
	}
	// we start resampling, sample by sample
	for len(samples) > 0 {
	reload:
		for c := range samples[0] {
			// calculate the current position in the original data
			j := float64(r.pos) * r.ratio

			// find quality*2 closest samples to j and translate them to points for interpolation
			for pi := 0; pi < len(r.pts); pi++ {
				// calculate the index of one of the closest samples
				k := int(j) + pi - len(r.pts)/2 + 1

				var y float64
				switch {
				// the sample is in buf1
				case k < r.off:
					y = r.buf1[len(r.buf1)+k-r.off][c]
				// the sample is in buf2
				case k < r.off+len(r.buf2):
					y = r.buf2[k-r.off][c]
				// the sample is beyond buf2, so we need to load new data
				case k >= r.off+len(r.buf2):
					// we load into buf1
					sn, _ := r.s.Stream(r.buf1)
					r.buf1 = r.buf1[:sn]
					// this condition happens when the original Streamer got
					// drained and j is after the end of the
					// original data
					if int(j) >= r.off+len(r.buf2)+sn {
						return n, n > 0
					}
					// this condition happens when the original Streamer got
					// drained and this one of the closest samples is after the
					// end of the original data
					if k >= r.off+len(r.buf2)+sn {
						y = 0
						break
					}
					// otherwise everything is fine, we swap buffers and start
					// calculating the sample again
					r.off += len(r.buf2)
					r.buf1, r.buf2 = r.buf2, r.buf1
					goto reload
				}

				r.pts[pi] = point{float64(k), y}
			}

			// calculate the resampled sample using polynomial interpolation from the
			// quality*2 closest samples
			samples[0][c] = lagrange(r.pts, j)
		}
		samples = samples[1:]
		n++
		r.pos++
	}
	return n, true
}

func (r *resample) Err() error {
	return r.s.Err()
}

// lagrange calculates the value at x of a polynomial of order len(pts)+1 which goes through all
// points in pts
func lagrange(pts []point, x float64) (y float64) {
	y = 0.0
	for j := range pts {
		l := 1.0
		for m := range pts {
			if j == m {
				continue
			}
			l *= (x - pts[m].X) / (pts[j].X - pts[m].X)
		}
		y += pts[j].Y * l
	}
	return y
}

type point struct {
	X, Y float64
}
