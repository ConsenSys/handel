+ expectations with IDs: all ids starting form 0 without any holes between ids,
  show example maybe within ethereum context (separate package ?)
+ Potentially need to sign different things in parallel, Handel should be able
  to support this case
    - stop waiting from peers that sends you signature for different messages
+ require only signature + message for Handel
+ Do we need to Origin in the packet ? -> we should not need to check

+ CandidateCount / UpdatePeriod
    - periodic 

+ if already completed level, skip verification
 -- basic check in synchronous manner

+ put asynchronous pattern if verification strategy
    + 1 thread for signature verification
    + 1 thread for logic "waiting" on new verified sig
    + Check first if I have same signature (= same bitset) before verifying
  signature

+ optimizations:
    - check signatures that are "best" for us in priority

+ Network: send to  a list of identities  (potential optimizations for example
  only sign a message once if same for multiple identities)

+ Adapt it to NON POWER OF TWO --> see partitioner test failing with non-power
  of two

+ potential callback on processing to verify packet signature


+ What to do when we receive BEST signature for lower levels ?
