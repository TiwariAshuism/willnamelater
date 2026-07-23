package com.example.quikcollabai

interface Platform {
    val name: String
}

expect fun getPlatform(): Platform