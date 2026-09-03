// imapControls lists the controls that are locked while a connection test runs.
const imapControls = [
    "imaphost",
    "imapport",
    "imapusername",
    "imappassword",
    "use_imap",
    "use_tls",
    "ignorecerterrors",
    "folder",
    "restrictdomain",
    "deletecampaign",
    "lastlogin",
    "imapfreq",
    "validateimap",
]

let imapLoadGeneration = 0
let imapSaveRequest

function setIMAPControlsDisabled(disabled) {
    imapControls.forEach(function (id) {
        document.getElementById(id).disabled = disabled
    })
}

function serializeSettingsForm(form) {
    var encoded = []
    var formData = new FormData(form)
    formData.forEach(function (value, name) {
        // Native FormData includes files, while jQuery serialize() did not.
        if (typeof value != "string") {
            return
        }
        encoded.push(
            encodeURIComponent(name) + "=" +
            encodeURIComponent(value.replace(/\r?\n/g, "\r\n"))
        )
    })
    return encoded.join("&").replace(/%20/g, "+")
}

function parseSettingsResponse(response) {
    var contentType = response.headers.get("Content-Type") || ""
    if (contentType.toLowerCase().indexOf("json") != -1) {
        return response.json()
    }
    return response.text()
}

function postSettingsForm(form) {
    return fetch("/settings", {
        method: "POST",
        credentials: "same-origin",
        headers: {
            "Accept": "*/*",
            "Content-Type": "application/x-www-form-urlencoded; charset=UTF-8",
        },
        body: serializeSettingsForm(form),
    }).then(function (response) {
        return parseSettingsResponse(response).then(function (data) {
            if (!response.ok) {
                throw new APIRequestError(response.statusText || "Request failed", {
                    status: response.status,
                    statusText: response.statusText,
                    data: data,
                    responseText: typeof data == "string" ? data : JSON.stringify(data),
                })
            }
            return data
        })
    }, function (error) {
        throw new APIRequestError("Network request failed", {
            status: 0,
            statusText: "error",
            responseText: "",
            cause: error,
        })
    })
}

// The application scripts are plain classic scripts at the end of <body>, so
// the document is still parsing when they run and DOMContentLoaded has not
// fired yet.
document.addEventListener('DOMContentLoaded', function () {
    bsInitTooltips();
    let revealedAPIKey = ""
    const clearAPIKeyReveal = function () {
        revealedAPIKey = ""
        document.getElementById("apiKeyRevealValue").replaceChildren()
        document.getElementById("apiKeyReveal").classList.add("d-none")
    }
    document.getElementById("closeApiKeyReveal").addEventListener("click", clearAPIKeyReveal)
    document.getElementById("copyApiKey").addEventListener("click", function () {
        if (revealedAPIKey && navigator.clipboard) {
            navigator.clipboard.writeText(revealedAPIKey)
        }
    })
    document.getElementById("apiResetForm").addEventListener("submit", function (e) {
        // The previous handler returned false, which cancelled the browser's
        // own submission as well as further propagation.
        e.preventDefault()
        e.stopPropagation()
        api.reset()
            .then(function (response) {
                successFlash(response.message)
                revealedAPIKey = response.data.api_key
                document.getElementById("apiKeyRevealValue").textContent = revealedAPIKey
                document.getElementById("apiKeyReveal").classList.remove("d-none")
            }, function (error) {
                errorFlash(requestErrorMessage(error))
            })
    })
    document.getElementById("settingsForm").addEventListener("submit", function (e) {
        e.preventDefault()
        e.stopPropagation()
        postSettingsForm(e.currentTarget)
            .then(function (data) {
                successFlash(data.message)
            }, function (error) {
                errorFlash(requestErrorMessage(error))
            })
    })
    document.getElementById("savesettings").addEventListener("click", function() {
        if (imapSaveRequest) {
            return false
        }

        var imapSettings = {}
        imapSettings.host = document.getElementById("imaphost").value
        imapSettings.port = document.getElementById("imapport").value
        imapSettings.username = document.getElementById("imapusername").value
        imapSettings.password = document.getElementById("imappassword").value
        imapSettings.enabled = document.getElementById("use_imap").checked
        imapSettings.tls = document.getElementById("use_tls").checked

        //Advanced settings
        imapSettings.folder = document.getElementById("folder").value
        imapSettings.imap_freq = document.getElementById("imapfreq").value
        imapSettings.restrict_domain = document.getElementById("restrictdomain").value
        imapSettings.ignore_cert_errors = document.getElementById("ignorecerterrors").checked
        imapSettings.delete_reported_campaign_email = document.getElementById("deletecampaign").checked

        //To avoid unmarshalling error in controllers/api/imap.go. It would fail gracefully, but with a generic error.
        if (imapSettings.host == ""){
            errorFlash("No IMAP Host specified")
            document.body.scrollTop = 0;
            document.documentElement.scrollTop = 0;
            return false
        }
        if (imapSettings.port == ""){
            errorFlash("No IMAP Port specified")
            document.body.scrollTop = 0;
            document.documentElement.scrollTop = 0;
            return false
        }
        if (isNaN(imapSettings.port) || imapSettings.port <1 || imapSettings.port > 65535  ){
            errorFlash("Invalid IMAP Port")
            document.body.scrollTop = 0;
            document.documentElement.scrollTop = 0;
            return false
        }
        if (imapSettings.imap_freq == ""){
            imapSettings.imap_freq = "60"
        }

        imapSaveRequest = api.IMAP.post(imapSettings).then(function (data) {
                if (data.success == true) {
                    successFlashFade("Successfully updated IMAP settings.", 2)
                } else {
                    errorFlash("Unable to update IMAP settings.")
                }
                // This is the second legacy done callback and must run after feedback.
                loadIMAPSettings()
            }, function (error) {
                errorFlash(requestErrorMessage(error))
            }).finally(function (){
                document.body.scrollTop = 0;
                document.documentElement.scrollTop = 0;
                imapSaveRequest = undefined
            })

        return false
    })

    document.getElementById("validateimap").addEventListener("click", function() {

        // Query validate imap server endpoint
        var server = {}
        server.host = document.getElementById("imaphost").value
        server.port = document.getElementById("imapport").value
        server.username = document.getElementById("imapusername").value
        server.password = document.getElementById("imappassword").value
        server.tls = document.getElementById("use_tls").checked
        server.ignore_cert_errors = document.getElementById("ignorecerterrors").checked

        //To avoid unmarshalling error in controllers/api/imap.go. It would fail gracefully, but with a generic error.
        if (server.host == ""){
            errorFlash("No IMAP Host specified")
            document.body.scrollTop = 0;
            document.documentElement.scrollTop = 0;
            return false
        }
        if (server.port == ""){
            errorFlash("No IMAP Port specified")
            document.body.scrollTop = 0;
            document.documentElement.scrollTop = 0;
            return false
        }
        if (isNaN(server.port) || server.port <1 || server.port > 65535  ){
            errorFlash("Invalid IMAP Port")
            document.body.scrollTop = 0;
            document.documentElement.scrollTop = 0;
            return false
        }

        var oldHTML = document.getElementById("validateimap").innerHTML;
        // Disable inputs and change button text
        setIMAPControlsDisabled(true);
        document.getElementById("validateimap").innerHTML = "<i class='fa fa-circle-o-notch fa-spin'></i> Testing...";

        api.IMAP.validate(server).then(function(data) {
            if (data.success == true) {
                Swal.fire({
                    title: "Success",
                    html: "Logged into <b>" + escapeHtml(document.getElementById("imaphost").value) + "</b>",
                    type: "success",
                })
            } else {
                Swal.fire({
                    title: "Failed!",
                    html: "Unable to login to <b>" + escapeHtml(document.getElementById("imaphost").value) + "</b>.",
                    type: "error",
                    showCancelButton: true,
                    cancelButtonText: "Close",
                    confirmButtonText: "More Info",
                    confirmButtonColor: "#428bca",
                    allowOutsideClick: false,
                }).then(function(result) {
                    if (result.value) {
                        Swal.fire({
                            title: "Error:",
                            text: data.message,
                        })
                    }
                  })
            }

          }, function() {
            Swal.fire({
                title: "Failed!",
                text: "An unecpected error occured.",
                type: "error",
            })
          })
          .finally(function() {
            //Re-enable inputs and change button text
            setIMAPControlsDisabled(false);
            document.getElementById("validateimap").innerHTML = oldHTML;

          });

      }); //end testclick

    document.getElementById("reporttab").addEventListener("click", function() {
        loadIMAPSettings()
    })

    document.getElementById("advanced").addEventListener("click", function() {
        // The markup hides this with an inline style, so the toggle flips
        // between that and letting the stylesheet decide.
        var area = document.getElementById("advancedarea")
        var hidden = window.getComputedStyle(area).display === "none"
        area.style.display = hidden ? "" : "none"
    })

    function loadIMAPSettings(){
        var loadGeneration = ++imapLoadGeneration
        api.IMAP.get().then(function (imap) {
            if (loadGeneration != imapLoadGeneration) {
                return
            }
            var lastLoginRow = document.getElementById("lastlogindiv")
            document.getElementById("imappassword").value = ""
            if (imap.length == 0){
                lastLoginRow.style.display = "none"
            } else {
                imap = imap[0]
                if (imap.enabled == false){
                    lastLoginRow.style.display = "none"
                } else {
                    lastLoginRow.style.display = ""
                }
                document.getElementById("imapusername").value = imap.username
                document.getElementById("imaphost").value = imap.host
                document.getElementById("imapport").value = imap.port
                document.getElementById("use_tls").checked = imap.tls
                document.getElementById("ignorecerterrors").checked = imap.ignore_cert_errors
                document.getElementById("use_imap").checked = imap.enabled
                document.getElementById("folder").value = imap.folder
                document.getElementById("restrictdomain").value = imap.restrict_domain
                document.getElementById("deletecampaign").checked = imap.delete_reported_campaign_email
                document.getElementById("lastloginraw").value = imap.last_login
                document.getElementById("lastlogin").value = moment.utc(imap.last_login).fromNow()
                document.getElementById("imapfreq").value = imap.imap_freq
            }

        }, function () {
            if (loadGeneration != imapLoadGeneration) {
                return
            }
            errorFlash("Error fetching IMAP settings")
        })
    }

    var use_map = localStorage.getItem('gophish.use_map')
    var mapToggle = document.getElementById("use_map")
    mapToggle.checked = JSON.parse(use_map)
    mapToggle.addEventListener('change', function () {
        localStorage.setItem('gophish.use_map', JSON.stringify(this.checked))
    })

    loadIMAPSettings()
})
