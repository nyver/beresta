// Package presentation defines the shared, locale-free presentation
// contract that every client (Windows desktop, Android) derives its UI
// status from. Types here are closed enums plus bounded scalar facts
// aggregated from core/account, core/sync, core/transport, and
// core/backup; they never carry localized strings, note content, titles,
// raw backend error text, or other note-derived data. User-visible text
// stays in each platform's localization catalog, keyed off these values.
package presentation
