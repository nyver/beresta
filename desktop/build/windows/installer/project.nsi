Unicode true

!include "wails_tools.nsh"

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName" "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright" "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName" "${INFO_PRODUCTNAME}"

ManifestDPIAware true

!include "MUI.nsh"
!include "LogicLib.nsh"
!include "nsDialogs.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
UninstPage custom un.DataPageCreate un.DataPageLeave
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Russian"

LangString UninstallDataTitle ${LANG_ENGLISH} "Local encrypted data"
LangString UninstallDataTitle ${LANG_RUSSIAN} "Локальные зашифрованные данные"
LangString UninstallDataDescription ${LANG_ENGLISH} "Choose whether uninstalling also removes this Windows user's local Beresta data."
LangString UninstallDataDescription ${LANG_RUSSIAN} "Выберите, нужно ли при удалении приложения удалить локальные данные Beresta этого пользователя Windows."
LangString UninstallDataCheckbox ${LANG_ENGLISH} "Permanently delete local accounts, notes, attachments, and settings"
LangString UninstallDataCheckbox ${LANG_RUSSIAN} "Безвозвратно удалить локальные аккаунты, заметки, вложения и настройки"
LangString UninstallDataWarning ${LANG_ENGLISH} "External backup folders are never removed. This local-data deletion cannot be undone."
LangString UninstallDataWarning ${LANG_RUSSIAN} "Внешние папки резервных копий никогда не удаляются. Удаление локальных данных нельзя отменить."
LangString InstallWriteFailed ${LANG_ENGLISH} "Beresta could not be installed. The prior executable was restored when available."
LangString InstallWriteFailed ${LANG_RUSSIAN} "Не удалось установить Beresta. Предыдущий исполняемый файл восстановлен, если он был доступен."

!uninstfinalize 'powershell.exe -NoProfile -ExecutionPolicy Bypass -File "..\sign-artifact.ps1" "%1"' = 0
!finalize 'powershell.exe -NoProfile -ExecutionPolicy Bypass -File "..\sign-artifact.ps1" "%1"' = 0

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
!ifdef WAILS_INSTALL_SCOPE
  !if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
  !else
    InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
  !endif
!else
  InstallDir "$PROGRAMFILES64\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"
!endif
ShowInstDetails show
ShowUninstDetails show

Var PriorExecutableAvailable
Var PurgeUserData
Var PurgeUserDataCheckbox

Function .onInit
  !insertmacro wails.checkArchitecture
  IfSilent done
    !insertmacro MUI_LANGDLL_DISPLAY
  done:
FunctionEnd

Function un.onInit
  StrCpy $PurgeUserData ${BST_UNCHECKED}
  IfSilent 0 interactive
    ${GetParameters} $R0
    ClearErrors
    ${GetOptions} $R0 "/PURGEUSERDATA=" $R1
    ${IfNot} ${Errors}
      ${If} $R1 == "1"
        StrCpy $PurgeUserData ${BST_CHECKED}
      ${EndIf}
    ${EndIf}
    Goto done
  interactive:
    !insertmacro MUI_LANGDLL_DISPLAY
  done:
FunctionEnd

Function un.DataPageCreate
  IfSilent skip
  !insertmacro MUI_HEADER_TEXT "$(UninstallDataTitle)" "$(UninstallDataDescription)"
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}
  ${NSD_CreateCheckbox} 0 12u 100% 24u "$(UninstallDataCheckbox)"
  Pop $PurgeUserDataCheckbox
  ${NSD_SetState} $PurgeUserDataCheckbox ${BST_UNCHECKED}
  ${NSD_CreateLabel} 0 48u 100% 36u "$(UninstallDataWarning)"
  Pop $0
  nsDialogs::Show
  skip:
FunctionEnd

Function un.DataPageLeave
  IfSilent skip
  ${NSD_GetState} $PurgeUserDataCheckbox $PurgeUserData
  skip:
FunctionEnd

Section "Beresta" MainSection
  !insertmacro wails.setShellContext
  !insertmacro wails.webview2runtime

  SetOutPath $INSTDIR
  StrCpy $PriorExecutableAvailable "0"
  IfFileExists "$INSTDIR\${PRODUCT_EXECUTABLE}" 0 no_prior
    ClearErrors
    CopyFiles /SILENT "$INSTDIR\${PRODUCT_EXECUTABLE}" "$INSTDIR\${PRODUCT_EXECUTABLE}.previous"
    IfErrors no_prior 0
    StrCpy $PriorExecutableAvailable "1"
  no_prior:

  ClearErrors
  !insertmacro wails.files
  IfErrors install_failed 0
  File "..\..\bin\beresta-updater.exe"
  IfErrors install_failed 0

  CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  !insertmacro wails.associateFiles
  !insertmacro wails.associateCustomProtocols
  !insertmacro wails.writeUninstaller
  Goto install_done

  install_failed:
    ${If} $PriorExecutableAvailable == "1"
      ClearErrors
      CopyFiles /SILENT "$INSTDIR\${PRODUCT_EXECUTABLE}.previous" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    ${Else}
      Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    ${EndIf}
    IfSilent no_message
      MessageBox MB_ICONSTOP|MB_OK "$(InstallWriteFailed)"
    no_message:
    SetErrorLevel 1
    Abort
  install_done:
SectionEnd

Section "uninstall"
  !insertmacro wails.setShellContext

  ReadRegStr $0 HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "Beresta"
  StrCmp $0 '$"$INSTDIR\${PRODUCT_EXECUTABLE}$" --autostart' 0 +2
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "Beresta"

  Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
  Delete "$INSTDIR\${PRODUCT_EXECUTABLE}.previous"
  Delete "$INSTDIR\beresta-updater.exe"
  Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
  Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"
  !insertmacro wails.unassociateFiles
  !insertmacro wails.unassociateCustomProtocols
  !insertmacro wails.deleteUninstaller
  RMDir "$INSTDIR"

  ${If} $PurgeUserData == ${BST_CHECKED}
    RMDir /r "$APPDATA\Beresta"
  ${EndIf}
SectionEnd
