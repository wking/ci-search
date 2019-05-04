package main

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"

	"github.com/golang/glog"
)

func (o *options) handleChart(w http.ResponseWriter, req *http.Request) {
	index, err := o.parseRequest(req, "chart")
	if err != nil {
		http.Error(w, fmt.Sprintf("Bad input: %v", err), http.StatusBadRequest)
		return
	}

	counts := make(map[string]int, len(index.Search))
	var lastJob string
	err = executeGrep(req.Context(), o.generator, index, 1, func(name string, search string, matches []bytes.Buffer, moreLines int) {
		metadata, _ := o.metadata.MetadataFor(name)
		if metadata.JobURI == nil {
			return
		}

		uri := metadata.JobURI.String()
		if uri != lastJob {
			lastJob = uri
			counts[search] += 1
		}
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed search: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer := encodedWriter(w, req)
	defer writer.Close()

	err = htmlChart.Execute(writer, map[string]interface{}{
		"index":  index,
		"counts": counts,
	})
	if err != nil {
		glog.Errorf("Failed to execute chart template: %v", err)
	}
}

var htmlChart = template.Must(template.New("chart").Parse(`<!DOCTYPE html>
<html>
  <head>
    <title>OpenShift CI Search</title>
    <meta charset="UTF-8">
    <meta name="description" content="{{.index.Job}} failure rates: {{with $dot := .}}{{range $index, $element := $dot.index.Search}}{{if $index}}, {{end}}{{index $dot.counts $element}} {{$element}}{{end}}{{end}}" />
    <meta property="og:image" content="https://raw.githubusercontent.com/wking/openshift-release/debug-scripts/d3/deck.png" />
    <style type="text/css">
      html, body {
        margin: 0;
        padding: 0;
      }

      svg {
        display: block;
        position: absolute;
        margin: 0;
        width: 100%;
        height: 100%;
      }
    </style>
  </head>
  <body>
    <script src="https://d3js.org/d3.v5.min.js"></script>
    <script>
      var markRadius = 5;
      var markOpacity = 0.75;
      var axisPadding = 50;
      var yAxisPadding = 70;  // y-axis label needs a bit more space, with the y-axis tick labels sticking out further from the axis

      var svg = d3.select('body').append('svg')
        .attr('preserveAspectRatio', 'xMidYMid meet')
        .attr('xmlns', 'http://www.w3.org/2000/svg')
        .attr('xmlns:xlink', 'http://www.w3.org/1999/xlink');

      var timestampParse = d3.utcParse('%s');
      var isoParse = d3.utcParse('%Y-%m-%dT%H:%M:%S%Z');
      var isoFormat = d3.utcFormat('%Y-%m-%dT%H:%M:%SZ');

      var xScale = d3.scaleUtc();
      var yScale = d3.scaleLinear();

      var xAxis = d3.axisBottom(xScale);
      var yAxis = d3.axisLeft(yScale);

      var filter = '{{.index.Job}}';
      var dateRange = {{.index.MaxAge.Seconds}};  // in seconds

      // {
      //   "regexp-pattern": {
      //     "job-URI": [
      //       {
      //         "match": "string that matched",
      //       },
      //     ],
      //   },
      // }
      var regexps = new Map();

{{range .index.Search}}
      regexps.set('{{.}}', new Map());
{{- end}}

      var regexpColors = [
        '#800000',  // maroon
        '#fabebe',  // pink
        '#e6beff',  // lavender
        '#000075',  // navy
        '#4363d8',  // blue
        '#000000',  // black
        '#e6194B',  // red
        '#42d4f4',  // cyan
        '#f032e6',  // magenta
        '#469990',  // teal
        '#9A6324',  // brown
        '#aaffc3',  // mint
        '#911eb4',  // purple
        '#808000',  // olive
        '#ffd8b1',  // apricot
      ];
      var jobs = [];

      function regexpMatches(job) {
        var matches = new Map();
        regexps.forEach((regexpMatches, regexp) => {
          patternMatches = regexpMatches.get(job.url);
          if (patternMatches) {
            var matchArray = [];
            matches.set(regexp, matchArray);
            patternMatches.forEach(match => matchArray.push(match));
          }
        });
        return matches;
      }

      function color(job) {
        if (job.job && !job.job.match(filter)) {
          return;
        }

        var matches = regexpMatches(job);
        if (matches.size > 0) {
          var matchedColor;
          [...regexps.keys()].some((regexp, i) => {
            if (matches.get(regexp)) {
              matchedColor = regexpColors[i];
              return true;
            }
          });
          if (matchedColor) {
            return matchedColor;
          }
        }
        switch (job.state) {
        case 'aborted':
          return;
        case 'success':
          return '#a9a9a9';  // gray
        case 'failure':
          return '#f58231';  // orange
        case 'pending':
          return '#ffe119';  // yellow
        case 'triggered':
          return;  // we don't care about these
        default:
          console.log('unrecognized job state', job.state);
        }
      }

      function legendHighlight(datum, index) {
        this.style.setProperty('font-weight', 'bold');
        var regexp = [...regexps.keys()][index];  // FIXME: fix highlighting for "could not \\(wait for\\|get\\) build.*" and others with escapes
        if (regexp === undefined) {
          return;
        }
        svg.selectAll('a.job > circle[data-regexps*="' + regexp.replace(/"/g, '\\"') + '"]')
          .attr('r', 2 * markRadius);
      }

      function legendLowlight(datum, index) {
        this.style.setProperty('font-weight', 'normal');
        var regexp = [...regexps.keys()][index];
        if (regexp === undefined) {
          return;
        }
        svg.selectAll('a.job > circle[data-regexps*="' + regexp.replace(/"/g, '\\"') + '"]')
          .attr('r', markRadius);
      }

      function legendClick(datum, index) {
        var oldRegexp = [...regexps.keys()][index];
        var newRegexp = window.prompt('build-log regexp', oldRegexp);
        if (newRegexp === oldRegexp) {
          return;
        } else if (newRegexp === '') {
          regexps.delete(oldRegexp);
          return;
        }
        var entries = [];
        regexps.forEach((value, key) => {
          if (key === oldRegexp) {
            key = newRegexp;
            value = new Map();
          }
          entries.push([key, value]);
        });
        oldMap = regexps;
        regexps = new Map(entries);
        search();
      }

      function redraw(interval) {
        var height = window.innerHeight;
        var width = window.innerWidth;
        var minDate = new Date(Date.now() - dateRange * 1000);
        var data = jobs.filter(job => color(job) && job.started >= minDate);

        xScale.domain(d3.extent(data, job => job.started));
        yScale.domain([0, d3.max(data, job => job.duration)]);

        svg.selectAll('*').remove();

        if (data.length > 0) {
          var now = Math.max(d3.max(data, job => job.started), d3.max(data, job => job._finished));
          var xMax = xScale.domain()[1];
          var yMax = yScale.domain()[1];
          svg.append('line')
            .attr('x1', xScale(xMax))
            .attr('y1', yScale((now - xMax) / 60000))
            .attr('x2', xScale(now - yMax * 60000))
            .attr('y2', yScale(yMax))
            .attr('stroke', 'black')
            .attr('stroke-opacity', '0.25');
        }
        svg.selectAll('a.job')
          .data(data)
          .enter()
          .append('a')
            .classed('job', true)
            .attr('xlink:href', job => job.url)
          .append('circle')
            .attr('cx', job => xScale(job.started))
            .attr('cy', job => yScale(job.duration))
            .attr('r', markRadius)
            .attr('fill-opacity', markOpacity)
            .attr('fill', job => color(job))
            .attr('data-regexps', job => [...regexpMatches(job).keys()].join('||'))
          .append('title')
            .text(job => {
              if (!job.url) {
                return JSON.stringify(job, null, 2);
              }
              var matches = regexpMatches(job);
              if (matches.size > 0) {
                var matchStrings = [];
                matches.forEach(matchArray => {
                  matchArray.forEach(match => matchStrings.push(match.match));
                });
                return matchStrings.join('\n');
              }
              return job.state;
            });

        svg.append('g')
          .attr('transform', 'translate(0, ' + (height - axisPadding) + ')')
          .call(xAxis);
        svg.append('g')
          .attr('transform', 'translate(' + yAxisPadding + ', 0)')
          .call(yAxis);

        svg.append('text')
          .attr('x', width / 2)
          .attr('y', axisPadding / 2)
          .style('text-anchor', 'middle')
          .style('cursor', 'pointer')
          .on('click', () => {
             var newFilter = window.prompt('job name filter', filter);
             if (newFilter) {
               filter = newFilter;
               redraw();
             }
          })
          .text(data.length + ' recent ' + filter + ' jobs')
        var xLabel = svg.append('text')
          .attr('x', width / 2)
          .attr('y', height - axisPadding / 2)
          .attr('dy', '1em')
          .style('text-anchor', 'middle');
        if (data.length === 0) {
          xLabel.text('started');
        } else if (data.length === 1) {
          xLabel.text('started (' + isoFormat(data[0].started) + ')');
        } else {
          xLabel.text('started (' + isoFormat(data[0].started) + ' through ' + isoFormat(data[data.length - 1].started) + ')');
        }
        svg.append('text')
          .attr('y', yAxisPadding / 3)
          .attr('x', -height / 2)
          .attr('transform', 'rotate(-90)')
          .style('text-anchor', 'middle')
          .text('duration (minutes)');

        var totalFailures = data.filter(job => job.state === 'failure').length;
        var legend = [];
        [...regexps.keys()].forEach((regexp, i) => {
          var matchCount = data.filter(job => regexps.get(regexp).get(job.url)).length;
          legend.push({
            color: regexpColors[i],
            text: matchCount + ' (' + Math.round(matchCount / (totalFailures || 1) * 100)+ '% of all failures) ' + regexp,
          });
        });
        var matchCount = data.filter(job => job.state === 'failure' && regexpMatches(job).size === 0).length;
        legend.push({
          color: color({state: 'failure'}),
          text: matchCount + ' (' + Math.round(matchCount / (totalFailures || 1) * 100) + '% of all failures) other failures',
        });
        ['pending', 'success'].forEach(state => {
          matchCount = data.filter(job => job.state === state).length;
          legend.push({
            color: color({state: state}),
            text: matchCount + ' (' + Math.round(matchCount / (data.length || 1) * 100) + '% of jobs) ' + state,
          });
        });
        var dy = 20;
        var y = axisPadding;  // high
        //var y = height - axisPadding - dy * (legend.length + 1);  // low
        var gLegend = svg.selectAll('g.legend')
          .data(legend)
          .enter()
          .append('g')
            .classed('legend', true);
        gLegend.append('circle')
          .attr('cx', yAxisPadding + 2 * markRadius)
          .attr('cy', (entry, index) => y + (index + 1) * dy - markRadius)
          .attr('r', markRadius)
          .attr('fill-opacity', markOpacity)
          .attr('fill', entry => entry.color);
        gLegend.append('text')
          .attr('x', yAxisPadding + 4 * markRadius)
          .attr('y', (entry, index) => y + (index + 1) * dy)
          .text(entry => entry.text)
          .on('mouseover', legendHighlight)
          .on('mouseout', legendLowlight)
          .on('click', legendClick);

        if (interval) {
          window.setTimeout(refetch, interval, interval);
        }
      }

      function refetch(interval) {
        // Currently: Reason: CORS header ‘Access-Control-Allow-Origin’ missing
        // https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS/Errors/CORSMissingAllowOrigin
        //d3.json('https://prow.svc.ci.openshift.org/data.js')
        d3.json('jobs')
          .then(data => {
            var now = new Date()
            data.forEach(job => {
              job.started = timestampParse(job.started);
              if (job.finished === '') {
                job._finished = now;
              }  else {
                job._finished = isoParse(job.finished)
              }
              job.duration = (job._finished - job.started) / 60000;  // minutes
            });

            data.sort((a, b) => a.started - b.started);
            jobs = data;
            search(interval);
          })
          .catch(alert);
      }

      function resize() {
        var height = window.innerHeight;
        var width = window.innerWidth;

        svg
          .attr('width', width)
          .attr('height', height);

        xScale.range([yAxisPadding, width - axisPadding]);
        yScale.range([height - axisPadding, axisPadding]);

        redraw();
      }

      function search(interval) {
        var searchParams = new URLSearchParams();
        searchParams.append('name', filter);
        searchParams.append('maxAge', dateRange + 's');  // chart is by start, but maxAge is by finish, so no need to expand this to handle drifting relative times.
        searchParams.append('context', 0);
        regexps.forEach((_, regexp) => {
          searchParams.append('search', regexp);
        });
        d3.json('search?' + searchParams)
          .then(data => {
            [...regexps.keys()].forEach(regexp => regexps.set(regexp, new Map()));
            for (var jobURI in data) {
              for (var regexp in data[jobURI]) {
                var matchArray = [];
                regexps.get(regexp).set(jobURI, matchArray);
                data[jobURI][regexp].forEach(match => matchArray.push({match: match.context[0]}));
              }
            }
            redraw(interval);
          })
          .catch(alert);
      }

      refetch(60000);
      resize();

      window.addEventListener('resize', resize);
      window.addEventListener('keyup', event => {
          if (event.key === 's') {
          const el = document.createElement('textarea');    // Create a <textarea> element
            el.value = svg.node().outerHTML;                // Set its value to the string that you want copied
            el.setAttribute('readonly', '');                // Make it readonly to be tamper-proof
            el.style.position = 'absolute';
            el.style.left = '-9999px';                      // Move outside the screen to make it invisible
            document.body.appendChild(el);                  // Append the <textarea> element to the HTML document
            const selected =
              document.getSelection().rangeCount > 0        // Check if there is any content selected previously
                ? document.getSelection().getRangeAt(0)     // Store selection if found
                : false;                                    // Mark as false to know no selection existed before
            el.select();                                    // Select the <textarea> content
            document.execCommand('copy');                   // Copy - only works as a result of a user action (e.g. click events)
            document.body.removeChild(el);                  // Remove the <textarea> element
            if (selected) {                                 // If a selection existed before copying
              document.getSelection().removeAllRanges();    // Unselect everything on the HTML document
              document.getSelection().addRange(selected);   // Restore the original selection
          }
        }
      });
    </script>
  </body>
</html>
`))
