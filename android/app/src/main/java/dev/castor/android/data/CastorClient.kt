package dev.castor.android.data

import com.castor.v1.CastRequest
import com.castor.v1.CastServiceClient
import com.castor.v1.Cast
import com.castor.v1.Device
import com.castor.v1.DeviceServiceClient
import com.castor.v1.ListCastsRequest
import com.castor.v1.ListDevicesRequest
import com.castor.v1.StopRequest
import com.castor.v1.WatchRequest
import com.castor.v1.WatchResponse
import com.connectrpc.ProtocolClientConfig
import com.connectrpc.ResponseMessage
import com.connectrpc.extensions.GoogleJavaLiteProtobufStrategy
import com.connectrpc.impl.ProtocolClient
import com.connectrpc.okhttp.ConnectOkHttpClient
import com.connectrpc.protocols.NetworkProtocol
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import okhttp3.Interceptor
import okhttp3.OkHttpClient
import java.util.concurrent.TimeUnit

/** A castor API server, spoken to over the Connect protocol on OkHttp with a bearer token. */
class CastorClient(private val baseUrl: String, token: String) {
    private val bearer = Interceptor { chain ->
        val request = chain.request().newBuilder().apply {
            if (token.isNotBlank()) header("Authorization", "Bearer $token")
        }.build()
        chain.proceed(request)
    }

    // Unary calls: discovery can take a few seconds, but nothing should hang for long.
    private val unaryHttp = OkHttpClient.Builder()
        .addInterceptor(bearer)
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    // Watch is a long-lived server stream: no read or call timeout.
    private val streamHttp = unaryHttp.newBuilder()
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .callTimeout(0, TimeUnit.MILLISECONDS)
        .build()

    private fun protocol(http: OkHttpClient) = ProtocolClient(
        httpClient = ConnectOkHttpClient(http),
        config = ProtocolClientConfig(
            host = baseUrl,
            serializationStrategy = GoogleJavaLiteProtobufStrategy(),
            networkProtocol = NetworkProtocol.CONNECT,
        ),
    )

    private val devices = DeviceServiceClient(protocol(unaryHttp))
    private val casts = CastServiceClient(protocol(unaryHttp))
    private val watchCasts = CastServiceClient(protocol(streamHttp))

    suspend fun listDevices(): List<Device> =
        devices.listDevices(ListDevicesRequest.getDefaultInstance()).unwrap().devicesList

    suspend fun cast(request: CastRequest): String = casts.cast(request).unwrap().castId

    suspend fun stop(castId: String) {
        casts.stop(StopRequest.newBuilder().setCastId(castId).build()).unwrap()
    }

    suspend fun listCasts(): List<Cast> =
        casts.listCasts(ListCastsRequest.getDefaultInstance()).unwrap().castsList

    /** Emits each Watch update; throws if the stream breaks (the server closes it after Ended). */
    fun watch(castId: String): Flow<WatchResponse> = flow {
        val stream = watchCasts.watch()
        try {
            stream.sendAndClose(WatchRequest.newBuilder().setCastId(castId).build())
            // The channel is closed with the stream's error, if any, so iterating throws on failure.
            for (message in stream.responseChannel()) emit(message)
        } finally {
            stream.receiveClose()
        }
    }
}

private fun <T : Any> ResponseMessage<T>.unwrap(): T = when (this) {
    is ResponseMessage.Success -> message
    is ResponseMessage.Failure -> throw cause
}

/** A message for people from whatever a call threw. */
fun Throwable.userMessage(): String {
    val m = message?.takeIf { it.isNotBlank() }
    return when (this) {
        is com.connectrpc.ConnectException -> when (code) {
            com.connectrpc.Code.UNAUTHENTICATED -> "Not authorized: check the token"
            else -> m ?: code.codeName
        }
        is java.net.UnknownHostException -> "Unknown host: check the server URL"
        is java.net.ConnectException, is java.net.SocketTimeoutException -> "Cannot reach the server (${m ?: javaClass.simpleName})"
        else -> m ?: javaClass.simpleName
    }
}
