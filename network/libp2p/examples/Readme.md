open 3 terminals
1)
terminal1: go run start.go -id 0 -reg 3
terminal2: go run start.go -id 1 -reg 3
terminal3: go run start.go -id 2 -reg 3

after around 30s the program deadlocs.

2)
go to: handel/network/libp2p/net.go

change 
 const protocol = "/handel/1.0.0"
to 
 const protocol = "/handel/1.0.0"

go to point 2
no deadloc anymore


