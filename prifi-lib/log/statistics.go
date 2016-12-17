package log

import (
"fmt"
"time"
"math"
	"github.com/dedis/cothority/log"
	"net/http"
)

const MAX_LATENCY_STORED = 100

type Statistics struct {
	begin			time.Time
	nextReport		time.Time
	nReports		int
	maxNReports		int
	period			time.Duration

	latencies				[]int64

	totalUpstreamCells		int64
	totalUpstreamBytes 		int64

	totalDownstreamCells 	int64
	totalDownstreamBytes 	int64

	instantUpstreamCells	int64
	instantUpstreamBytes 	int64
	instantDownstreamBytes	int64

	totalDownstreamUDPCells 	int64
	totalDownstreamUDPBytes 	int64
	instantDownstreamUDPBytes 	int64
	totalDownstreamUDPBytesTimesClients 	int64
	instantDownstreamUDPBytesTimesClients 	int64

	totalDownstreamRetransmitCells 	int64
	totalDownstreamRetransmitBytes 	int64
	instantDownstreamRetransmitBytes 	int64
}

func EmptyStatistics(reportingLimit int) *Statistics{
	stats := Statistics{time.Now(), time.Now(), 0, reportingLimit, time.Duration(5)*time.Second, make([]int64, 0), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	return &stats
}

func (stats *Statistics) ReportingDone() bool {
	if stats.maxNReports == 0 || stats.maxNReports == -1 {
		return false
	}
	return stats.nReports >= stats.maxNReports
}

func (stats *Statistics) Dump() {
	fmt.Println("Dumping Statistics...")
	fmt.Println("begin", stats.begin)
	fmt.Println("nextReport", stats.nextReport)
	fmt.Println("nReports", stats.nReports)
	fmt.Println("maxNReports", stats.maxNReports)
	fmt.Println("period", stats.period)

	fmt.Println(stats.totalUpstreamCells)
	fmt.Println(stats.totalUpstreamBytes)
	fmt.Println(stats.totalDownstreamCells)
	fmt.Println(stats.totalDownstreamBytes)
	fmt.Println(stats.totalDownstreamUDPCells)
	fmt.Println(stats.totalDownstreamUDPBytes)
	fmt.Println(stats.instantUpstreamCells)
	fmt.Println(stats.instantUpstreamBytes)
	fmt.Println(stats.instantDownstreamBytes)
	fmt.Println(stats.instantDownstreamUDPBytes)
	fmt.Println(stats.totalDownstreamRetransmitCells)
	fmt.Println(stats.totalDownstreamRetransmitBytes)
	fmt.Println(stats.instantDownstreamRetransmitBytes)
}

func round(f float64) float64 {
	return math.Floor(f + .5)
}

func round2(f float64, places int) float64 {
	shift := math.Pow(10, float64(places))
	return round(f * shift) / shift;
}

func mean(data []int64) float64 {
	sum := int64(0)
	for i:=0; i<len(data); i++ {
		sum += data[i]
	}

	mean := float64(sum) / float64(len(data))
	return mean
}

func mean2(data []float64) float64 {
	sum := float64(0)
	for i:=0; i<len(data); i++ {
		sum += data[i]
	}

	mean := float64(sum) / float64(len(data))
	return mean
}

func confidence(data []int64) float64 {

	if len(data) == 0{
		return 0
	}
	mean_val := mean(data)

	deviations := make([]float64, 0)
	for i:=0; i<len(data); i++ {
		diff := mean_val - float64(data[i])
		deviations = append(deviations, diff*diff)
	}

	std := mean2(deviations)
	stderr := math.Sqrt(std)
	z_value_95 := 1.96
	margin_error := stderr * z_value_95

	return margin_error
}

func (stats *Statistics) LatencyStatistics() (string, string, string) {

	if len(stats.latencies) == 0{
		return "-1", "-1", "-1"
	}

	m := round2(mean(stats.latencies), 2)
	v := round2(confidence(stats.latencies), 2)

	return fmt.Sprintf("%v", m), fmt.Sprintf("%v", v), fmt.Sprintf("%v", len(stats.latencies))
}

func (stats *Statistics) AddLatency(latency int64) {
	stats.latencies = append(stats.latencies, latency)

	//we remove the first items
	if len(stats.latencies) > MAX_LATENCY_STORED {
		start := len(stats.latencies) - MAX_LATENCY_STORED
		stats.latencies = stats.latencies[start:]
	}
}

func (stats *Statistics) AddDownstreamCell(nBytes int64) {
	stats.totalDownstreamCells += 1
	stats.totalDownstreamBytes += nBytes
	stats.instantDownstreamBytes += nBytes
}

func (stats *Statistics) AddDownstreamUDPCell(nBytes int64, nclients int) {
	stats.totalDownstreamUDPCells += 1
	stats.totalDownstreamUDPBytes += nBytes
	stats.instantDownstreamRetransmitBytes += nBytes

	stats.totalDownstreamUDPBytesTimesClients += (nBytes * int64(nclients))
	stats.instantDownstreamUDPBytesTimesClients += (nBytes * int64(nclients))
}

func (stats *Statistics) AddDownstreamRetransmitCell(nBytes int64) {
	stats.totalDownstreamRetransmitCells += 1
	stats.totalDownstreamRetransmitBytes += nBytes
	stats.instantDownstreamUDPBytes += nBytes
}

func (stats *Statistics) AddUpstreamCell(nBytes int64) {
	stats.totalUpstreamCells += 1
	stats.totalUpstreamBytes += nBytes
	stats.instantUpstreamCells += 1
	stats.instantUpstreamBytes += nBytes
}

func (stats *Statistics) Report() {
	stats.ReportWithInfo("")
}


func (stats *Statistics) ReportWithInfo(info string) {
	now := time.Now()
	if now.After(stats.nextReport) {
		//duration := now.Sub(stats.begin).Seconds()
		//latm, latv, latn := stats.LatencyStatistics()

		/*
		instantRetransmitPercentage := float64(0)
		if stats.instantDownstreamRetransmitBytes + stats.totalDownstreamUDPBytes != 0 {
			instantRetransmitPercentage = float64(100 * stats.instantDownstreamRetransmitBytes)/float64(stats.instantDownstreamUDPBytesTimesClients)
		}

		totalRetransmitPercentage := float64(0)
		if stats.instantDownstreamRetransmitBytes + stats.totalDownstreamUDPBytes != 0 {
			totalRetransmitPercentage = float64(100 * stats.totalDownstreamRetransmitBytes)/float64(stats.totalDownstreamUDPBytesTimesClients)
		}
		*/

		/*
		log.Lvlf1("%v @ %fs; cell %f (%f) /sec, up %f (%f) B/s, down %f (%f) B/s, udp down %f (%f) B/s, retransmit %v (%v), lat %s += %s over %s "+info,
			stats.nReports, duration,
			float64(stats.totalUpstreamCells)/duration, 	 float64(stats.instantUpstreamCells)/stats.period.Seconds(),
			float64(stats.totalUpstreamBytes)/duration,	 	 float64(stats.instantUpstreamBytes)/stats.period.Seconds(),
			float64(stats.totalDownstreamBytes)/duration, 	 float64(stats.instantDownstreamBytes)/stats.period.Seconds(),
			float64(stats.totalDownstreamUDPBytes)/duration, float64(stats.instantDownstreamUDPBytes)/stats.period.Seconds(),
			float64(totalRetransmitPercentage), 			 float64(instantRetransmitPercentage),
			latm, latv, latn)
			*/
		log.Lvlf1("%0.1f round/sec, %0.1f kB/s up, %0.1f kB/s down, %0.1f kB/s down(udp)",
			float64(stats.instantUpstreamCells) / stats.period.Seconds(),
			float64(stats.instantUpstreamBytes) / 1024 / stats.period.Seconds(),
			float64(stats.instantDownstreamBytes) / 1024 / stats.period.Seconds(),
			float64(stats.instantDownstreamUDPBytes) / 1024 / stats.period.Seconds())

		data := fmt.Sprintf("round=%0.1f&up=%0.1f&down=%0.1f&udp_down%0.1f",
			float64(stats.instantUpstreamCells) / stats.period.Seconds(),
			float64(stats.instantUpstreamBytes) / 1024 / stats.period.Seconds(),
			float64(stats.instantDownstreamBytes) / 1024 / stats.period.Seconds(),
			float64(stats.instantDownstreamUDPBytes) / 1024 / stats.period.Seconds())

		go performGETRequest("http://lbarman.ch/prifi/?" + data)



		// Next report time
		stats.instantUpstreamCells = 0
		stats.instantUpstreamBytes = 0
		stats.instantDownstreamBytes = 0
		stats.instantDownstreamUDPBytes = 0
		stats.instantDownstreamRetransmitBytes = 0
		stats.instantDownstreamUDPBytesTimesClients = 0

		stats.nextReport = now.Add(stats.period)
		stats.nReports += 1
	}
}

func performGETRequest(url string) {
	_, _ = http.Get(url)
}