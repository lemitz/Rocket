package dev.rocket.android

import java.io.IOException

// Only app-authored messages are shown directly. Framework/provider and PC errors
// can be in any language; the UI uses a localized fallback for those.
internal class RocketError(message: String, cause: Throwable? = null) : IOException(message, cause)
