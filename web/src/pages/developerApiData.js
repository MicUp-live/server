// developerApiData.js — Canonical developer endpoints, contract-aligned code samples, and wire fixtures.

export const DEVELOPER_ENDPOINTS = [
    {
        method: 'GET',
        path: '/api/v1/api-keys',
        desc: 'List all API keys for authenticated host',
        scope: 'Bearer / Session',
    },
    {
        method: 'POST',
        path: '/api/v1/api-keys',
        desc: 'Generate new Personal Access Token',
        scope: 'Bearer / Session',
    },
    {
        method: 'DELETE',
        path: '/api/v1/api-keys/{id}',
        desc: 'Revoke developer API key',
        scope: 'Bearer / Session',
    },
    {
        method: 'GET',
        path: '/api/v1/events',
        desc: 'List all events owned by host',
        scope: 'events:read',
    },
    {
        method: 'GET',
        path: '/api/v1/events/{id}/requests?since={v}',
        desc: 'Conditional host queue intake (all 6 statuses, pending singer requests; 204 if unchanged)',
        scope: 'requests:read',
    },
    {
        method: 'GET',
        path: '/api/v1/events/{id}/queue?since={v}',
        desc: 'Conditional public queue polling (accepted & up_next only; 204 if unchanged)',
        scope: 'Public',
    },
    {
        method: 'PATCH',
        path: '/api/v1/events/{id}/requests/{rid}',
        desc: 'Update status (canonical: accepted, up_next, completed, rejected; aliases: playing, declined)',
        scope: 'requests:write',
    },
    {
        method: 'POST',
        path: '/api/v1/events/{id}/pause',
        desc: 'Pause singer requests',
        scope: 'events:write',
    },
    {
        method: 'POST',
        path: '/api/v1/events/{id}/resume',
        desc: 'Resume singer requests',
        scope: 'events:write',
    },
]

export const CODE_SAMPLES = {
    curl: `# 1. List active events for this host
curl -s -H "Authorization: Bearer mk_live_YOUR_KEY" \\
  https://micup.live/api/v1/events

# 2. Poll live host requests (returns 204 No Content if unchanged)
curl -si -H "Authorization: Bearer mk_live_YOUR_KEY" \\
  "https://micup.live/api/v1/events/evt_01h7/requests?since=42"

# 3. Place a song up next (canonical status, optional atomic expected_queue_version)
curl -s -X PATCH -H "Authorization: Bearer mk_live_YOUR_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"status":"up_next","expected_queue_version":42}' \\
  https://micup.live/api/v1/events/evt_01h7/requests/req_01

# 4. Decline request with custom reason
curl -s -X PATCH -H "Authorization: Bearer mk_live_YOUR_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"status":"rejected","decline_reason":"Taking a 15-minute host break"}' \\
  https://micup.live/api/v1/events/evt_01h7/requests/req_01`,

    python: `import time
import requests

API_KEY = "mk_live_YOUR_KEY"
EVENT_ID = "evt_01h7x89q3m4k"
BASE_URL = "https://micup.live/api/v1"

headers = {"Authorization": f"Bearer {API_KEY}"}
queue_version = 0

print("🚀 Polling MicUp host queue for incoming singer requests...")
while True:
    try:
        res = requests.get(
            f"{BASE_URL}/events/{EVENT_ID}/requests?since={queue_version}",
            headers=headers,
            timeout=5
        )
        if res.status_code == 200:
            data = res.json()
            queue_version = data["queue_version"]
            for req in data.get("requests", []):
                if req["status"] == "pending":
                    print(f"👉 New Request: {req['singer_name']} sings {req['song_title']}")
        elif res.status_code == 204:
            # 0 bandwidth! Queue unchanged.
            pass
    except Exception as err:
        print(f"Poll error: {err}")

    time.sleep(3)`,

    csharp: `using System;
using System.Collections.Generic;
using System.Net;
using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json.Serialization;
using System.Threading.Tasks;

public record SingerRequest(
    [property: JsonPropertyName("id")] string Id,
    [property: JsonPropertyName("singer_name")] string SingerName,
    [property: JsonPropertyName("song_title")] string SongTitle,
    [property: JsonPropertyName("song_artist")] string SongArtist,
    [property: JsonPropertyName("status")] string Status
);

public record HostQueuePollResult(
    [property: JsonPropertyName("queue_version")] long QueueVersion,
    [property: JsonPropertyName("current_round")] int CurrentRound,
    [property: JsonPropertyName("requests")] List<SingerRequest> Requests
);

public class MicUpSyncService
{
    private readonly HttpClient _http;
    private readonly string _eventId;
    private long _queueVersion = 0;

    public MicUpSyncService(string apiKey, string eventId)
    {
        _eventId = eventId;
        _http = new HttpClient { BaseAddress = new Uri("https://micup.live/api/v1/") };
        _http.DefaultRequestHeaders.Authorization =
            new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", apiKey);
    }

    public async Task PollAsync(Action<SingerRequest> onNewRequest)
    {
        var res = await _http.GetAsync($"events/{_eventId}/requests?since={_queueVersion}");
        if (res.StatusCode == HttpStatusCode.NoContent) return; // 0 bandwidth!

        if (res.IsSuccessStatusCode)
        {
            var data = await res.Content.ReadFromJsonAsync<HostQueuePollResult>();
            if (data?.Requests == null) return;
            _queueVersion = data.QueueVersion;
            foreach (var req in data.Requests)
            {
                if (req.Status == "pending")
                {
                    onNewRequest(req);
                }
            }
        }
    }

    public async Task SetUpNextAsync(string requestId, long expectedQueueVersion)
    {
        var payload = new { status = "up_next", expected_queue_version = expectedQueueVersion };
        await _http.PatchAsJsonAsync($"events/{_eventId}/requests/{requestId}", payload);
    }
}`,
}
