package redis

// scripts.go holds the EventLog Lua scripts. The sequence counter, stream
// append and tombstone check must run as one atomic unit, since Redis
// scripts are the only atomicity the streams log has. The envelope is
// encoded in Go and passed verbatim; the allocated sequence lives only in
// the stream id and the seq field, so the persisted bytes are never
// re-serialized. Both scripts also stamp the run sweep index, so Expire
// visits only the runs that may hold an expired event.
//
// The index score is the run's oldest retained instant: a run scores below
// the expiry horizon exactly when it holds at least one expired event. A
// stamp therefore only lowers the score, never raises it, so a later append
// with a newer instant cannot hide an older entry the sweep must drop.

// eventAppendScript allocates the next sequence, appends the envelope and
// stamps the run index in one unit. A failed XADD after INCR may leave a
// sequence gap: the error already fails the caller and Redis scripts do
// not roll back.
//
// The index score is the run's oldest retained instant, so the stamp only
// lowers it: a later append with a newer instant must not hide an older
// entry the sweep still has to drop.
//
// KEYS[1] sequence counter, KEYS[2] run stream, KEYS[3] run index.
// ARGV[1] recorded milliseconds, ARGV[2] envelope bytes, ARGV[3] capacity,
// ARGV[4] run id.
const eventAppendScript = `
local seq = redis.call('INCR', KEYS[1])
local args = {'XADD', KEYS[2]}
if tonumber(ARGV[3]) > 0 then
	args[#args + 1] = 'MAXLEN'
	args[#args + 1] = ARGV[3]
end
args[#args + 1] = tostring(seq) .. '-0'
args[#args + 1] = 'seq'
args[#args + 1] = tostring(seq)
args[#args + 1] = 't'
args[#args + 1] = ARGV[1]
args[#args + 1] = 'e'
args[#args + 1] = ARGV[2]
redis.call(unpack(args))
local score = redis.call('ZSCORE', KEYS[3], ARGV[4])
if not score or tonumber(ARGV[1]) < tonumber(score) then
	redis.call('ZADD', KEYS[3], ARGV[1], ARGV[4])
end
return seq
`

// eventCompactionScript checks the publication tombstone and, for a new
// identity only, allocates the sequence, records the fingerprint, appends
// the stamped event and stamps the run index in one unit.
//
// KEYS[1] sequence counter, KEYS[2] run stream, KEYS[3] tombstone hash,
// KEYS[4] run index.
// ARGV[1] publication id, ARGV[2] fingerprint, ARGV[3] recorded
// milliseconds, ARGV[4] envelope bytes, ARGV[5] capacity, ARGV[6] run id.
// Returns {new, stored-fingerprint}: new is 1 only when the identity was
// never seen before.
const eventCompactionScript = `
local existing = redis.call('HGET', KEYS[3], ARGV[1])
if existing then
	return {0, existing}
end
local seq = redis.call('INCR', KEYS[1])
redis.call('HSET', KEYS[3], ARGV[1], ARGV[2])
local args = {'XADD', KEYS[2]}
if tonumber(ARGV[5]) > 0 then
	args[#args + 1] = 'MAXLEN'
	args[#args + 1] = ARGV[5]
end
args[#args + 1] = tostring(seq) .. '-0'
args[#args + 1] = 'seq'
args[#args + 1] = tostring(seq)
args[#args + 1] = 't'
args[#args + 1] = ARGV[3]
args[#args + 1] = 'e'
args[#args + 1] = ARGV[4]
redis.call(unpack(args))
local score = redis.call('ZSCORE', KEYS[4], ARGV[6])
if not score or tonumber(ARGV[3]) < tonumber(score) then
	redis.call('ZADD', KEYS[4], ARGV[3], ARGV[6])
end
return {1, ARGV[2]}
`

// firstSeqScript returns the oldest retained sequence and the counter in
// one atomic step: {-1, next} when the stream holds nothing.
const firstSeqScript = `
local next = redis.call('GET', KEYS[1])
local entries = redis.call('XRANGE', KEYS[2], '-', '+', 'COUNT', 1)
if #entries > 0 then
	local seq = string.match(entries[1][1], '^(%d+)-')
	return {tonumber(seq), tonumber(next) or 0}
end
return {-1, tonumber(next) or 0}
`
