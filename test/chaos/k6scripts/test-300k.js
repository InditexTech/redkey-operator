// SPDX-FileCopyrightText: 2025 INDUSTRIA DE DISEÑO TEXTIL, S.A. (INDITEX, S.A.)
//
// SPDX-License-Identifier: Apache-2.0

/*
  Test that imitates a redis-cluster that was causing rebalancing errors:
  - Value Size: 1Byte - 300KBytes
  - Timeout: 30s
  - Some deletes
 */

import redis from 'k6/x/redis';
import { randomBytes } from 'k6/crypto';
import { check, sleep } from 'k6';

export const options = {
    thresholds: {
        checks: ['rate>0.99'],
    },
};

// Build cluster seed nodes from REDIS_HOSTS ("host:port,host:port,..."). The first entry is the
// cluster's headless Service FQDN (<cluster>.<ns>.svc.cluster.local), whose A-record always resolves
// to the *current, ready* pod IPs; the remaining entries are the per-pod DNS names as fallbacks.
// Seeding from the Service lets go-redis rediscover the live topology after a StatefulSet recreate
// (e.g. PurgeKeysOnRebalance), instead of staying wedged on pods that no longer exist.
//
// NOTE: xk6-redis interprets these socket timeouts in MILLISECONDS. They must be long enough for a
// fresh TCP+RESP handshake to a freshly-scheduled pod to succeed (so the cluster client can run
// CLUSTER SLOTS and reload its slot->node topology) but short enough that a VU stuck on a dead pod
// fails within a couple of seconds and recycles, instead of blocking for the 5s go-redis default
// multiplied by the pool/redirect retries.
function createClient() {
    const nodes = __ENV.REDIS_HOSTS.split(',').map(node => {
        const [host, port] = node.split(':');
        return {
            socket: {
                host,
                port: Number(port) || 6379,
                dialTimeout: 1000,  // 1s: enough to handshake a recovering pod, fails fast on a dead one
                readTimeout: 2000,  // 2s: cover reads of values up to ~300KB
                writeTimeout: 2000, // 2s: cover writes of values up to ~300KB
            },
        };
    });

    return new redis.Client({
        cluster: {
            // Follow a few redirects so commands track slots as they migrate during rebalancing, and
            // route randomly so a single stale node does not stall every request.
            maxRedirects: 4,
            routeRandomly: true,
            nodes,
        },
    });
}

let client = createClient();

// When chaos replaces every pod, go-redis keeps using the IPs it discovered via CLUSTER SLOTS and is
// slow to fall back to re-resolving the DNS seeds once all of those cached IPs are dead, so it can
// stay wedged dialing stale addresses. Rebuilding the client forces a fresh re-resolution of the
// seeds (the headless Service FQDN first) and rediscovery of the live topology. Throttle rebuilds so
// a burst of failing VUs does not thrash DNS/connections. xk6 runs each VU in its own JS runtime, so
// `client` and these counters are per-VU.
let lastReconnectAt = 0;
const reconnectCooldownMs = 5000;

function maybeReconnect() {
    const now = Date.now();
    if (now - lastReconnectAt < reconnectCooldownMs) {
        return;
    }
    lastReconnectAt = now;
    client = createClient();
}

// Helper function to generate random-sized values
function generateRandomValue(maxBytes) {
    const size = Math.floor(Math.random() * maxBytes) + 1; // Random size from 1 to maxBytes
    return randomBytes(size).toString('base64');
}

export default async function () {
    const uniqueKey = `mykey_${__VU}_${__ITER}`;
    const value = generateRandomValue(300000);

    try {
        const setResult = await client.set(uniqueKey, value, 30);
        const setOk = check(setResult, {
            'redis set succeeds': (result) => result === 'OK',
        });
        if (!setOk) {
            const message = `[K6_ERROR] set failed for ${uniqueKey}`;
            console.error(message);
            throw new Error(message);
        }

        // Randomly delete approximately 1 in 10 keys
        if (Math.random() < 0.1) {
            const deleted = await client.del(uniqueKey);
            const deleteOk = check(deleted, {
                'redis delete succeeds': (result) => result === 1,
            });
            if (!deleteOk) {
                const message = `[K6_ERROR] delete failed for ${uniqueKey}; result=${deleted}`;
                console.error(message);
                throw new Error(message);
            }
        } else {
            const retrievedValue = await client.get(uniqueKey);
            const getOk = check(retrievedValue, {
                'redis get returns original value': (result) => result === value,
            });
            if (!getOk) {
                const resultLength = retrievedValue ? retrievedValue.length : 'null';
                const message = `[K6_ERROR] get failed for ${uniqueKey}; length=${resultLength}`;
                console.error(message);
                throw new Error(message);
            }
        }
    } catch (error) {
        const message = `[K6_ERROR] iteration failed for ${uniqueKey}: ${error}`;
        console.error(message);
        // The client has likely cached now-dead pod IPs; rebuild it (throttled) so the next
        // iterations rediscover the live topology instead of hammering stale addresses.
        maybeReconnect();
        throw error;
    }

    // Sleep for a short random duration to simulate real-world load patterns
    sleep(Math.random() * 0.1); // Sleep for up to 100ms
}
