# kotlinx.serialization generates serializers reflectively at the edges; keep
# the generated companions so release builds don't fail to parse API responses.
-keepclassmembers class ** {
    *** Companion;
}
-keepclasseswithmembers class ** {
    kotlinx.serialization.KSerializer serializer(...);
}
-keep,includedescriptorclasses class ng.proxify.vpn.data.**$$serializer { *; }
-keepclassmembers class ng.proxify.vpn.data.** {
    *** Companion;
}

# The WireGuard backend is reached through JNI.
-keep class com.wireguard.android.backend.** { *; }
-keep class com.wireguard.crypto.** { *; }
