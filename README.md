# ssh-honeypot

## Deleting points
```sh
docker run -it --rm influxdb influx delete --org-id $INFLUXDB_ORG --bucket $INFLUXDB_BUCKET --host $INFLUXDB_URL --token $INFLUXDB_TOKEN --start '2009-01-02T23:00:00Z' --stop '2029-01-02T23:00:00Z' --predicate 'ip="::1"'
```

##  Grafana Geo Map
```
from(bucket: "ssh-honeypot")
    |> range(start: v.timeRangeStart, stop: v.timeRangeStop)
    |> filter(fn: (r) => r["_field"] == "latitude" or r["_field"] == "longitude")
    |> aggregateWindow(every: v.windowPeriod, fn: mean, createEmpty: false)
    |> pivot(rowKey:["_time"], columnKey: ["_field"], valueColumn: "_value")
    |> group()
```