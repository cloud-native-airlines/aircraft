// Package flight holds the pure flight calculation: given a plan and a simulated
// time it computes the aircraft's physical state. It has no HTTP or network
// dependencies so it can be tested in isolation.
package flight

import "math"

const earthRadiusM = 6371008.8 // mean Earth radius (meters), IUGG

// Point is a geographic coordinate in decimal degrees.
type Point struct {
	Lat float64
	Lon float64
}

func radians(deg float64) float64 { return deg * math.Pi / 180 }
func degrees(rad float64) float64 { return rad * 180 / math.Pi }

// DistanceM returns the great-circle distance between two points in meters,
// using the haversine formula.
func DistanceM(a, b Point) float64 {
	lat1, lat2 := radians(a.Lat), radians(b.Lat)
	dLat := lat2 - lat1
	dLon := radians(b.Lon - a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// BearingDeg returns the initial great-circle bearing from a to b, in degrees
// in [0, 360). If the points coincide it returns 0.
func BearingDeg(a, b Point) float64 {
	lat1, lat2 := radians(a.Lat), radians(b.Lat)
	dLon := radians(b.Lon - a.Lon)
	y := math.Sin(dLon) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLon)
	if y == 0 && x == 0 {
		return 0
	}
	return math.Mod(degrees(math.Atan2(y, x))+360, 360)
}

// Interpolate returns the point a fraction f (0..1) of the way along the
// great-circle route from a to b (spherical linear interpolation / slerp).
func Interpolate(a, b Point, f float64) Point {
	switch {
	case f <= 0:
		return a
	case f >= 1:
		return b
	}

	lat1, lon1 := radians(a.Lat), radians(a.Lon)
	lat2, lon2 := radians(b.Lat), radians(b.Lon)

	// Angular distance between the points.
	d := DistanceM(a, b) / earthRadiusM
	if d == 0 {
		return a
	}
	sinD := math.Sin(d)

	ka := math.Sin((1-f)*d) / sinD
	kb := math.Sin(f*d) / sinD

	x := ka*math.Cos(lat1)*math.Cos(lon1) + kb*math.Cos(lat2)*math.Cos(lon2)
	y := ka*math.Cos(lat1)*math.Sin(lon1) + kb*math.Cos(lat2)*math.Sin(lon2)
	z := ka*math.Sin(lat1) + kb*math.Sin(lat2)

	lat := math.Atan2(z, math.Sqrt(x*x+y*y))
	lon := math.Atan2(y, x)
	return Point{Lat: degrees(lat), Lon: degrees(lon)}
}
