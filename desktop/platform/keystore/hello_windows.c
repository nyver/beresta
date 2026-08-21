//go:build windows

#define COBJMACROS
#include <windows.h>
#include <roapi.h>
#include <winstring.h>
#include <inspectable.h>
#include <asyncinfo.h>
#include <windows.security.credentials.ui.h>
#include <stdint.h>

typedef struct BerestaUserConsentVerifierInterop BerestaUserConsentVerifierInterop;

typedef struct BerestaUserConsentVerifierInteropVtbl {
    HRESULT (STDMETHODCALLTYPE *QueryInterface)(BerestaUserConsentVerifierInterop *, REFIID, void **);
    ULONG (STDMETHODCALLTYPE *AddRef)(BerestaUserConsentVerifierInterop *);
    ULONG (STDMETHODCALLTYPE *Release)(BerestaUserConsentVerifierInterop *);
    HRESULT (STDMETHODCALLTYPE *GetIids)(BerestaUserConsentVerifierInterop *, ULONG *, IID **);
    HRESULT (STDMETHODCALLTYPE *GetRuntimeClassName)(BerestaUserConsentVerifierInterop *, HSTRING *);
    HRESULT (STDMETHODCALLTYPE *GetTrustLevel)(BerestaUserConsentVerifierInterop *, TrustLevel *);
    HRESULT (STDMETHODCALLTYPE *RequestVerificationForWindowAsync)(
        BerestaUserConsentVerifierInterop *, HWND, HSTRING, REFIID, void **);
} BerestaUserConsentVerifierInteropVtbl;

struct BerestaUserConsentVerifierInterop {
    const BerestaUserConsentVerifierInteropVtbl *lpVtbl;
};

static const IID beresta_iid_user_consent_verifier_interop = {
    0x39e050c3, 0x4e74, 0x441a, {0x8d, 0xc0, 0xb8, 0x11, 0x04, 0xdf, 0x94, 0x9c}
};
static const IID beresta_iid_user_consent_verifier_statics = {
    0xaf4f3f91, 0x564c, 0x4ddc, {0xb8, 0xb5, 0x97, 0x34, 0x47, 0x62, 0x7c, 0x65}
};
static const IID beresta_iid_async_verification = {
    0xfd596ffd, 0x2318, 0x558f, {0x9d, 0xbe, 0xd2, 0x1d, 0xf4, 0x37, 0x64, 0xa5}
};
static const IID beresta_iid_async_availability = {
    0xddd384f3, 0xd818, 0x5d83, {0xab, 0x4b, 0x32, 0x11, 0x9c, 0x28, 0x58, 0x7c}
};
static const IID beresta_iid_async_info = {
    0x00000036, 0x0000, 0x0000, {0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}
};
static const wchar_t beresta_user_consent_class[] =
    L"Windows.Security.Credentials.UI.UserConsentVerifier";

static HRESULT beresta_wait_async(IUnknown *operation, HANDLE cancel_event) {
    IAsyncInfo *info = NULL;
    HRESULT hr = operation->lpVtbl->QueryInterface(operation, &beresta_iid_async_info, (void **)&info);
    if (FAILED(hr)) {
        return hr;
    }

    for (;;) {
        AsyncStatus status = Started;
        hr = info->lpVtbl->get_Status(info, &status);
        if (FAILED(hr)) {
            break;
        }
        if (status == Completed) {
            hr = S_OK;
            break;
        }
        if (status == Canceled) {
            hr = HRESULT_FROM_WIN32(ERROR_CANCELLED);
            break;
        }
        if (status == Error) {
            hr = E_FAIL;
            if (FAILED(info->lpVtbl->get_ErrorCode(info, &hr))) {
                hr = E_FAIL;
            }
            break;
        }
        if (cancel_event != NULL && WaitForSingleObject(cancel_event, 15) == WAIT_OBJECT_0) {
            info->lpVtbl->Cancel(info);
            hr = HRESULT_FROM_WIN32(ERROR_CANCELLED);
            break;
        }
        Sleep(15);
    }

    info->lpVtbl->Release(info);
    return hr;
}

static void beresta_close_async(IUnknown *operation) {
    IAsyncInfo *info = NULL;
    if (operation == NULL) {
        return;
    }
    if (SUCCEEDED(operation->lpVtbl->QueryInterface(operation, &beresta_iid_async_info, (void **)&info))) {
        info->lpVtbl->Close(info);
        info->lpVtbl->Release(info);
    }
}

static HRESULT beresta_create_class_name(HSTRING *class_name) {
    return WindowsCreateString(
        beresta_user_consent_class,
        (UINT32)(sizeof(beresta_user_consent_class) / sizeof(wchar_t) - 1),
        class_name);
}

HRESULT beresta_hello_available(uintptr_t cancel_event_value, INT32 *result) {
    if (result == NULL) {
        return E_INVALIDARG;
    }
    *result = -1;

    HRESULT init_hr = RoInitialize(RO_INIT_MULTITHREADED);
    BOOL uninitialize = SUCCEEDED(init_hr);
    if (FAILED(init_hr) && init_hr != RPC_E_CHANGED_MODE) {
        return init_hr;
    }

    HSTRING class_name = NULL;
    HRESULT hr = beresta_create_class_name(&class_name);
    __x_ABI_CWindows_CSecurity_CCredentials_CUI_CIUserConsentVerifierStatics *statics = NULL;
    __FIAsyncOperation_1_UserConsentVerifierAvailability *operation = NULL;
    if (SUCCEEDED(hr)) {
        hr = RoGetActivationFactory(
            class_name,
            &beresta_iid_user_consent_verifier_statics,
            (void **)&statics);
    }
    if (SUCCEEDED(hr)) {
        hr = statics->lpVtbl->CheckAvailabilityAsync(statics, &operation);
    }
    if (SUCCEEDED(hr)) {
        hr = beresta_wait_async((IUnknown *)operation, (HANDLE)cancel_event_value);
    }
    if (SUCCEEDED(hr)) {
        __x_ABI_CWindows_CSecurity_CCredentials_CUI_CUserConsentVerifierAvailability availability;
        hr = operation->lpVtbl->GetResults(operation, &availability);
        if (SUCCEEDED(hr)) {
            *result = (INT32)availability;
        }
    }

    if (operation != NULL) {
        beresta_close_async((IUnknown *)operation);
        operation->lpVtbl->Release(operation);
    }
    if (statics != NULL) statics->lpVtbl->Release(statics);
    if (class_name != NULL) WindowsDeleteString(class_name);
    if (uninitialize) RoUninitialize();
    return hr;
}

HRESULT beresta_hello_verify(
    uintptr_t window_value,
    const wchar_t *message,
    UINT32 message_length,
    uintptr_t cancel_event_value,
    INT32 *result) {
    HWND window = (HWND)window_value;
    if (window == NULL || message == NULL || message_length == 0 || result == NULL) {
        return E_INVALIDARG;
    }
    *result = -1;

    HRESULT init_hr = RoInitialize(RO_INIT_MULTITHREADED);
    BOOL uninitialize = SUCCEEDED(init_hr);
    if (FAILED(init_hr) && init_hr != RPC_E_CHANGED_MODE) {
        return init_hr;
    }

    HSTRING class_name = NULL;
    HSTRING prompt = NULL;
    HRESULT hr = beresta_create_class_name(&class_name);
    BerestaUserConsentVerifierInterop *interop = NULL;
    __FIAsyncOperation_1_UserConsentVerificationResult *operation = NULL;
    if (SUCCEEDED(hr)) {
        hr = WindowsCreateString(message, message_length, &prompt);
    }
    if (SUCCEEDED(hr)) {
        hr = RoGetActivationFactory(
            class_name,
            &beresta_iid_user_consent_verifier_interop,
            (void **)&interop);
    }
    if (SUCCEEDED(hr)) {
        hr = interop->lpVtbl->RequestVerificationForWindowAsync(
            interop,
            window,
            prompt,
            &beresta_iid_async_verification,
            (void **)&operation);
    }
    if (SUCCEEDED(hr)) {
        hr = beresta_wait_async((IUnknown *)operation, (HANDLE)cancel_event_value);
    }
    if (SUCCEEDED(hr)) {
        __x_ABI_CWindows_CSecurity_CCredentials_CUI_CUserConsentVerificationResult verification;
        hr = operation->lpVtbl->GetResults(operation, &verification);
        if (SUCCEEDED(hr)) {
            *result = (INT32)verification;
        }
    }

    if (operation != NULL) {
        beresta_close_async((IUnknown *)operation);
        operation->lpVtbl->Release(operation);
    }
    if (interop != NULL) interop->lpVtbl->Release(interop);
    if (prompt != NULL) WindowsDeleteString(prompt);
    if (class_name != NULL) WindowsDeleteString(class_name);
    if (uninitialize) RoUninitialize();
    return hr;
}
