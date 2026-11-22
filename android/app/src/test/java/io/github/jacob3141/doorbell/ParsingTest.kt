package io.github.jacob3141.doorbell

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ParsingTest {
    private fun bytes(vararg v: Int) = ByteArray(v.size) { v[it].toByte() }

    // The keyframe and the delta frame that the hub tests use as well.
    private val key = bytes(0, 0, 0, 1, 0x67, 0x4d, 0x00, 0x28, 0xaa, 0, 0, 0, 1, 0x68, 0xee, 0, 0, 0, 1, 0x65, 0x11, 0x22)
    private val delta = bytes(0, 0, 0, 1, 0x41, 0x33, 0x44)

    @Test
    fun splitsNalUnits() {
        val u = AnnexB.units(key)
        assertEquals(listOf(AnnexB.SPS, AnnexB.PPS, AnnexB.SLICE_IDR), u.map { AnnexB.type(key, it) })
        assertEquals(4 until 9, u[0]) // the zero of the next 4-byte start code is not part of the SPS
        assertEquals(listOf(0x41), AnnexB.units(delta).map { delta[it.first].toInt() })
    }

    @Test
    fun threeByteStartCodes() {
        val b = bytes(0, 0, 1, 0x67, 1, 2, 0, 0, 1, 0x68, 3, 0, 0, 1, 0x65, 4)
        assertEquals(listOf(AnnexB.SPS, AnnexB.PPS, AnnexB.SLICE_IDR), AnnexB.units(b).map { AnnexB.type(b, it) })
    }

    @Test
    fun keyframesAndParameterSets() {
        assertTrue(AnnexB.isKeyframe(key))
        assertFalse(AnnexB.isKeyframe(delta))
        val ps = AnnexB.parameterSets(key)!!
        assertArrayEquals(bytes(0, 0, 0, 1, 0x67, 0x4d, 0x00, 0x28, 0xaa), ps.first)
        assertArrayEquals(bytes(0, 0, 0, 1, 0x68, 0xee), ps.second)
        assertNull(AnnexB.parameterSets(delta))
    }

    @Test
    fun parsesHubEvents() {
        assertEquals(HubEvent.Ring("ab12", 1763750000), Rpc.parse("""{"jsonrpc":"2.0","method":"ring","params":{"id":"ab12","time":1763750000}}"""))
        assertEquals(HubEvent.RingTaken("ab12", "Pixel"), Rpc.parse("""{"jsonrpc":"2.0","method":"ring.taken","params":{"id":"ab12","by":"Pixel"}}"""))
        assertEquals(HubEvent.RingMissed("ab12"), Rpc.parse("""{"jsonrpc":"2.0","method":"ring.missed","params":{"id":"ab12"}}"""))
        assertEquals(HubEvent.MessageNew("20261121-183000", 5), Rpc.parse("""{"jsonrpc":"2.0","method":"message.new","params":{"id":"20261121-183000","time":5}}"""))
        assertEquals(HubEvent.Response(3, true, null), Rpc.parse("""{"jsonrpc":"2.0","id":3,"result":{"video":"/api/video"}}"""))
        assertEquals(HubEvent.Response(4, false, "Ring is no longer active"), Rpc.parse("""{"jsonrpc":"2.0","id":4,"error":{"code":-32000,"message":"Ring is no longer active"}}"""))
        assertEquals(HubEvent.Unknown, Rpc.parse("not json"))
        assertEquals(HubEvent.Unknown, Rpc.parse("""{"jsonrpc":"2.0","method":"something.else"}"""))
        assertEquals(HubEvent.Unknown, Rpc.parse("""{"jsonrpc":"2.0","method":"ring","params":{}}"""))
    }

    @Test
    fun buildsRequests() {
        val m = org.json.JSONObject(Rpc.request(7, "ring.accept", "ab12"))
        assertEquals("2.0", m.getString("jsonrpc"))
        assertEquals(7, m.getLong("id"))
        assertEquals("ring.accept", m.getString("method"))
        assertEquals("ab12", m.getJSONObject("params").getString("id"))
    }

    @Test
    fun normalizesHubAddresses() {
        assertEquals("http://doorbell-hub.home.arpa", HubAddress.normalize("doorbell-hub.home.arpa"))
        assertEquals("http://10.0.3.10:9000", HubAddress.normalize(" http://10.0.3.10:9000/ "))
        assertEquals("http://hub.home.arpa:8088", HubAddress.normalize("hub.home.arpa:8088"))
        assertEquals("http://[fd4a::1]", HubAddress.normalize("[fd4a::1]"))
        assertEquals("http://[fd4a::1]", HubAddress.normalize("[fd4a::1]:80"))
        assertNull(HubAddress.normalize(""))
        assertNull(HubAddress.normalize("https://hub"))
        assertNull(HubAddress.normalize("hub/admin"))
        assertNull(HubAddress.normalize("hub:99999"))
        assertNull(HubAddress.normalize("user@hub"))
        assertEquals("ws://doorbell-hub.home.arpa/api/events", HubAddress.ws("http://doorbell-hub.home.arpa", "/api/events"))
    }

    @Test
    fun parsesHubTimes() {
        assertEquals(java.time.Instant.parse("2025-11-21T17:30:00.123Z").toEpochMilli(), HubApi.parseTime("2025-11-21T18:30:00.123+01:00"))
        assertEquals(0, HubApi.parseTime("garbage"))
    }
}
