package dev.rocket.android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class DownloadStoreTest {
    @Test
    fun downloadNameValidationRejectsUnsafeNames() {
        listOf(
            "",
            ".",
            "..",
            "../file.txt",
            "folder\\file.txt",
            "line\nbreak.txt",
            "nul\u0000name.txt",
            "delete\u007fname.txt",
            "x".repeat(201)
        ).forEach { name -> assertFalse("Expected rejection for $name", isSafeDownloadName(name)) }
    }

    @Test
    fun downloadNameValidationAcceptsSafeNamesAtLimit() {
        assertTrue(isSafeDownloadName("report.txt"))
        assertTrue(isSafeDownloadName("x".repeat(200)))
        assertTrue(isSafeDownloadName("résumé-😀.txt"))
    }

    @Test
    fun duplicateSelectionAddsNumberBeforeExtension() {
        val existing = setOf("report.txt", "report (1).txt")
        assertEquals("report (2).txt", uniqueDownloadName("report.txt", existing::contains))
    }

    @Test
    fun duplicateSelectionHandlesExtensionlessAndDotNames() {
        assertEquals("archive (1)", uniqueDownloadName("archive") { it == "archive" })
        assertEquals(".config (1)", uniqueDownloadName(".config") { it == ".config" })
    }

    @Test
    fun duplicateSelectionReturnsOriginalWhenAvailable() {
        assertEquals("report.txt", uniqueDownloadName("report.txt") { false })
    }

    @Test
    fun duplicateSelectionReportsExhaustion() {
        assertNull(uniqueDownloadName("occupied") { true })
    }
}
