package dev.rocket.android

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class RocketProtocolTest {
    @Test
    fun validMessageEnforcesUtf16CharacterLimit() {
        assertTrue(validMessage("a".repeat(8192)))
        assertFalse(validMessage("a".repeat(8193)))
    }

    @Test
    fun validMessageEnforcesUtf8ByteLimit() {
        assertTrue(validMessage("€".repeat(2730))) // 8190 UTF-8 bytes
        assertFalse(validMessage("€".repeat(2731))) // 8193 UTF-8 bytes
        assertTrue(validMessage("😀".repeat(2048))) // exactly 8192 UTF-8 bytes
        assertFalse(validMessage("😀".repeat(2049))) // 8196 UTF-8 bytes
    }

    @Test
    fun validMessageRejectsMalformedSurrogatePairs() {
        assertFalse(validMessage("\uD800"))
        assertFalse(validMessage("\uDC00"))
        assertFalse(validMessage("left\uD800right"))
        assertFalse(validMessage("\uDC00\uD800"))
        assertTrue(validMessage("\uD83D\uDE00"))
    }

    @Test
    fun validMessageRejectsEmptyAndNulText() {
        assertFalse(validMessage(""))
        assertFalse(validMessage("before\u0000after"))
    }
}
