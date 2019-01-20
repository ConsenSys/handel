package monitor

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dedis/onet/log"
	"github.com/montanaflynn/stats"
)

// Stats contains all structures that are related to the computations of stats
// such as Value (compute the mean/min/max/...), Measurements ( aggregation of
// Value), Stats (collection of measurements) and PercentileFilter which is used to
// apply some filtering before any statistics is done.

// Stats holds the different measurements done
type Stats struct {
	// The static fields are created when creating the stats out of a
	// running config.
	static     map[string]string
	staticKeys []string

	// The received measures we have and the keys ordered
	values map[string]*Value
	keys   []string

	filter DataFilter
	sync.Mutex
}

// NewStats return a Stats with the given defaults values. For example:
// { "nodes": "10", "simul": "funny_one" }. If df is nil, no filter is taken.
func NewStats(defs map[string]string, df DataFilter) *Stats {
	s := new(Stats).init()
	s.setDefaultValues(defs)
	if df != nil {
		s.filter = df
	}
	// TODO
	// let the filter figure out itself what it is supposed to be doing
	// s.filter = NewDataFilter(rc)
	return s
}

func (s *Stats) init() *Stats {
	s.values = make(map[string]*Value)
	s.keys = make([]string, 0)
	s.static = make(map[string]string)
	s.staticKeys = make([]string, 0)
	return s
}

// Update will update the Stats with this given measure
func (s *Stats) Update(m *singleMeasure) {
	s.Lock()
	defer s.Unlock()
	var value *Value
	var ok bool
	value, ok = s.values[m.Name]
	if !ok {
		value = NewValue(m.Name)
		s.values[m.Name] = value
		s.keys = append(s.keys, m.Name)
		sort.Strings(s.keys)
	}
	value.Store(m.Value)
}

// WriteHeader will write the header to the writer
func (s *Stats) WriteHeader(w io.Writer) {
	s.Lock()
	defer s.Unlock()
	// write static  fields
	var fields []string
	for _, k := range s.staticKeys {
		fields = append(fields, k)
	}
	// Write the values header
	for _, k := range s.keys {
		v := s.values[k]
		fields = append(fields, v.HeaderFields()...)
	}
	fmt.Fprintf(w, "%s", strings.Join(fields, ","))
	fmt.Fprintf(w, "\n")
}

// WriteValues will write the values to the specified writer
func (s *Stats) WriteValues(w io.Writer) {
	// by default
	s.Collect()
	s.Lock()
	defer s.Unlock()
	// write static fields
	var values []string
	for _, k := range s.staticKeys {
		if v, ok := s.static[k]; ok {
			values = append(values, v)
		}
	}
	// write the values
	for _, k := range s.keys {
		v := s.values[k]
		values = append(values, v.Values()...)
	}
	fmt.Fprintf(w, "%s", strings.Join(values, ","))
	fmt.Fprintf(w, "\n")
}

// WriteIndividualStats will write the values to the specified writer but without
// making averages. Each value should either be:
//   - represented once - then it'll be copied to all runs
//   - have the same frequency as the other non-once values
func (s *Stats) WriteIndividualStats(w io.Writer) error {
	// by default
	s.Lock()
	defer s.Unlock()

	// Verify we have either one or n values, where n >= 1 but constant
	// over all values
	n := 1
	for _, k := range s.keys {
		if newN := len(s.values[k].store); newN > 1 {
			if n == 1 {
				n = newN
			} else if n != newN {
				return errors.New("found inconsistencies in values")
			}
		}
	}

	// store static fields
	var static []string
	for _, k := range s.staticKeys {
		if v, ok := s.static[k]; ok {
			static = append(static, v)
		}
	}

	// add all values
	for entry := 0; entry < n; entry++ {
		var values []string
		// write the values
		for _, k := range s.keys {
			v := s.values[k]
			values = append(values, v.SingleValues(entry)...)
		}

		all := append(static, values...)
		_, err := fmt.Fprintf(w, "%s", strings.Join(all, ","))
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(w, "\n")
		if err != nil {
			return err
		}

	}
	return nil
}

// AverageStats will make an average of the given stats
func AverageStats(stats []*Stats) *Stats {
	if len(stats) < 1 {
		return new(Stats)
	}
	s := new(Stats).init()
	stats[0].Lock()
	s.filter = stats[0].filter
	s.static = stats[0].static
	s.staticKeys = stats[0].staticKeys
	s.keys = stats[0].keys
	stats[0].Unlock()
	// Average
	for _, k := range s.keys {
		var values []*Value
		for _, stat := range stats {
			stat.Lock()
			value, ok := stat.values[k]
			if !ok {
				continue
			}
			values = append(values, value)
			stat.Unlock()
		}
		// make the average
		avg := AverageValue(values...)
		// don't have to necessary collect or filters here. Collect() must be called only
		// when we want the final results (writing or by calling Value(name)
		s.values[k] = avg
	}
	return s
}

// DataFilter is a generic interface that can filter data according to some
// rules. For example, filter out everything outside the 90-th percentile.
type DataFilter interface {
	Filter(measure string, values []float64) []float64
}

// PercentileFilter is used to process data before making any statistics about them
type PercentileFilter struct {
	// percentiles maps the measurements name to the percentile we need to take
	// to filter thoses measuremements with the percentile
	percentiles map[string]float64
}

// NewPercentileFilter returns a percentile filter that will filter all values
// belonging to keys given in the map, by the specified amount of the
// percentile.
func NewPercentileFilter(toFilter map[string]float64) PercentileFilter {
	df := PercentileFilter{
		percentiles: toFilter,
	}
	log.Lvl3("Filtering:", df.percentiles)
	return df
}

// Filter out a serie of values
func (df *PercentileFilter) Filter(measure string, values []float64) []float64 {
	// do we have a filter for this measure ?
	if _, ok := df.percentiles[measure]; !ok {
		return values
	}
	// Compute the percentile value
	max, err := stats.PercentileNearestRank(values, df.percentiles[measure])
	if err != nil {
		log.Lvl2("Monitor: Error filtering data(", values, "):", err)
		return values
	}

	// Find the index from where to filter
	maxIndex := -1
	for i, v := range values {
		if v > max {
			maxIndex = i
		}
	}
	// check if we foud something to filter out
	if maxIndex == -1 {
		log.Lvl3("Filtering: nothing to filter for", measure)
		return values
	}
	// return the values below the percentile
	log.Lvl3("Filtering: filters out", measure, ":", maxIndex, "/", len(values))
	return values[:maxIndex]
}

// Collect make the final computations before stringing or writing.
// Automatically done in other methods anyway.
func (s *Stats) Collect() {
	s.Lock()
	defer s.Unlock()
	for _, v := range s.values {
		if s.filter != nil {
			v.Filter(s.filter)
		}
		v.Collect()
	}
}

// Value returns the value object corresponding to this name in this Stats
func (s *Stats) Value(name string) *Value {
	s.Lock()
	defer s.Unlock()
	if val, ok := s.values[name]; ok {
		return val
	}
	return nil
}

// Returns an overview of the stats - not complete data returned!
func (s *Stats) String() string {
	s.Collect()
	s.Lock()
	defer s.Unlock()
	var str string
	for _, k := range s.staticKeys {
		str += fmt.Sprintf("%s = %v ", k, s.static[k])
	}
	for _, v := range s.values {
		str += fmt.Sprintf("%v ", v.Values())
	}
	return fmt.Sprintf("{Stats: %s}", str)
}

// setDefaultValues stores these default values to always be written in the
// output stats
func (s *Stats) setDefaultValues(defaults map[string]string) {
	// First find the defaults keys
	for key, val := range defaults {
		s.static[key] = val
		s.staticKeys = append(s.staticKeys, key)
	}
	// sort them so it's always the same order
	sort.Strings(s.staticKeys)
}

// Value is used to compute the statistics
// it represent the time to an action (setup, shamir round, coll round etc)
// use it to compute streaming mean + dev
type Value struct {
	name string
	min  float64
	max  float64
	sum  float64
	n    int
	oldM float64
	newM float64
	oldS float64
	newS float64
	dev  float64

	// Store where are kept the values
	store []float64
	sync.Mutex
}

// NewValue returns a new value object with this name
func NewValue(name string) *Value {
	return &Value{name: name, store: make([]float64, 0)}
}

// Store takes this new time and stores it for later analysis
// Since we might want to do percentile sorting, we need to have all the Values
// For the moment, we do a simple store of the Value, but note that some
// streaming percentile algorithm exists in case the number of messages is
// growing to big.
func (t *Value) Store(newTime float64) {
	t.Lock()
	defer t.Unlock()
	t.store = append(t.store, newTime)
}

// Collect will collect all float64 stored in the store's Value and will compute
// the basic statistics about them such as min, max, dev and avg.
func (t *Value) Collect() {
	t.Lock()
	defer t.Unlock()
	// It is kept as a streaming average / dev processus for the moment (not the most
	// optimized).
	// streaming dev algo taken from http://www.johndcook.com/blog/standard_deviation/
	t.sum = 0
	for _, newTime := range t.store {
		// nothings takes 0 ms to complete, so we know it's the first time
		if t.min > newTime || t.n == 0 {
			t.min = newTime
		}
		if t.max < newTime {
			t.max = newTime
		}

		t.n++
		if t.n == 1 {
			t.oldM = newTime
			t.newM = newTime
			t.oldS = 0.0
		} else {
			t.newM = t.oldM + (newTime-t.oldM)/float64(t.n)
			t.newS = t.oldS + (newTime-t.oldM)*(newTime-t.newM)
			t.oldM = t.newM
			t.oldS = t.newS
		}
		t.dev = math.Sqrt(t.newS / float64(t.n-1))
		t.sum += newTime
	}
}

// Filter outs its Values
func (t *Value) Filter(filt DataFilter) {
	t.Lock()
	defer t.Unlock()
	t.store = filt.Filter(t.name, t.store)
}

// AverageValue will create a Value averaging all Values given
func AverageValue(st ...*Value) *Value {
	if len(st) < 1 {
		return new(Value)
	}
	var t Value
	name := st[0].name
	for _, s := range st {
		if s.name != name {
			log.Error("Averaging not the sames Values ...?")
			return new(Value)
		}
		s.Lock()
		t.store = append(t.store, s.store...)
		s.Unlock()
	}
	t.name = name
	return &t
}

// Min returns the minimum of all stored float64
func (t *Value) Min() float64 {
	t.Lock()
	defer t.Unlock()
	return t.min
}

// Max returns the maximum of all stored float64
func (t *Value) Max() float64 {
	t.Lock()
	defer t.Unlock()
	return t.max
}

// Sum returns the sum of all stored float64
func (t *Value) Sum() float64 {
	t.Lock()
	defer t.Unlock()
	return t.sum
}

// NumValue returns the number of Value added
func (t *Value) NumValue() int {
	t.Lock()
	defer t.Unlock()
	return t.n
}

// Avg returns the average (mean) of the Values
func (t *Value) Avg() float64 {
	t.Lock()
	defer t.Unlock()
	return t.newM
}

// Dev returns the standard deviation of the Values
func (t *Value) Dev() float64 {
	t.Lock()
	defer t.Unlock()
	return t.dev
}

// HeaderFields returns the first line of the CSV-file
func (t *Value) HeaderFields() []string {
	return []string{t.name + "_min", t.name + "_max", t.name + "_avg", t.name + "_sum", t.name + "_dev"}
}

// Values returns the string representation of a Value
func (t *Value) Values() []string {
	return []string{
		strconv.FormatFloat(t.min, 'g', 4, 64),
		strconv.FormatFloat(t.Max(), 'g', 4, 64),
		strconv.FormatFloat(t.Avg(), 'g', 4, 64),
		strconv.FormatFloat(t.Sum(), 'g', 4, 64),
		strconv.FormatFloat(t.Dev(), 'g', 4, 64)}
}

// SingleValues returns the string representation of an entry in the value
func (t *Value) SingleValues(i int) []string {
	v := fmt.Sprintf("%f", t.store[0])
	if i < len(t.store) {
		v = fmt.Sprintf("%f", t.store[i])
	}
	return []string{v, v, v, v, "NaN"}
}

func (t *Value) String() string {
	return fmt.Sprintf("{%s_avg: %f}", t.name, t.Avg())
}
