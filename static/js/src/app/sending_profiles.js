// The previous selector escaped the dot in this id, which defeats jQuery's
// getElementById fast path, so it matched every element carrying the id rather
// than the first.
function clearModalFlashes() {
    document.querySelectorAll('[id="modal.flashes"]').forEach(function (container) {
        container.replaceChildren()
    })
}

// submitHandler holds the listener currently bound to the modal's submit
// button, which is reused for every record.
var submitHandler = null

function bindModalSubmit(handler) {
    // More than one element can carry the id "modalSubmit", one per modal. The
    // previous selector took jQuery's getElementById fast path and so bound
    // only the first, which is the page modal's own button.
    var submit = document.getElementById("modalSubmit")
    if (submitHandler) {
        submit.removeEventListener("click", submitHandler)
    }
    submitHandler = handler
    submit.addEventListener("click", submitHandler)
}

var profiles = []
var latestProfileRequest = 0
var latestProfileLoadRequest = 0
var saveRequest = null
var activeProfileId = null
var refreshProfileModal = null

// headers holds the DataTables instance backing the modal's custom-header list.
// It is recreated every time the modal opens, which is what the destroy option
// did before.
var headers = null

function setProfileFormDisabled(disabled) {
    ["name", "from", "host", "username", "password", "ignore_cert_errors",
        "headerKey", "headerValue", "addCustomHeader", "modalSubmit"]
        .forEach(function (id) {
            document.getElementById(id).disabled = disabled
        })
    document.querySelector('#modal button[onclick^="bsModalShow(\'#sendTestEmailModal\'"]').disabled = disabled
    var headersContainer = document.getElementById("headersTable_wrapper")
        || document.getElementById("headersTable")
    headersContainer.inert = disabled
}

function enableProfileFormAfterPendingSave(profileRequest) {
    if (!saveRequest) {
        setProfileFormDisabled(false)
        return
    }
    var pendingSave = saveRequest
    setProfileFormDisabled(true)
    var enableCurrentForm = function () {
        if (profileRequest == latestProfileRequest) {
            setProfileFormDisabled(false)
        }
    }
    pendingSave.then(enableCurrentForm, enableCurrentForm)
}

// Attempts to send a test email by POSTing to /campaigns/
function sendTestEmail() {
    // Named apart from the module-level DataTables instance this reads from.
    var headerRecords = [];
    headers.rows().data().toArray().forEach(function (header) {
        headerRecords.push({
            key: unescapeHtml(header[0]),
            value: unescapeHtml(header[1]),
        })
    })
    var test_email_request = {
        template: {},
        first_name: document.querySelector("input[name=to_first_name]").value,
        last_name: document.querySelector("input[name=to_last_name]").value,
        email: document.querySelector("input[name=to_email]").value,
        position: document.querySelector("input[name=to_position]").value,
        url: '',
        smtp: {
            interface_type: document.getElementById("interface_type").value,
            from_address: document.getElementById("from").value,
            host: document.getElementById("host").value,
            username: document.getElementById("username").value,
            password: document.getElementById("password").value,
            ignore_cert_errors: document.getElementById("ignore_cert_errors").checked,
            headers: headerRecords,
        }
    }
    if (activeProfileId != null) {
        test_email_request.smtp.id = activeProfileId
        test_email_request.smtp.name = document.getElementById("name").value
    }
    btnHtml = document.getElementById("sendTestModalSubmit").innerHTML
    document.getElementById("sendTestModalSubmit").innerHTML = '<i class="fa fa-spinner fa-spin"></i> Sending'
    // Send the test email
    api.send_test_email(test_email_request)
        .then(function () {
            showTestEmailFlash("alert-success", "fa-check-circle", "Email Sent!")
            document.getElementById("sendTestModalSubmit").innerHTML = btnHtml
        }, function (error) {
            showTestEmailFlash("alert-danger", "fa-exclamation-circle", requestErrorMessage(error))
            document.getElementById("sendTestModalSubmit").innerHTML = btnHtml
        })
}

// Save attempts to POST or PUT a sending profile.
function save(profileId) {
    if (saveRequest) {
        return
    }
    var profile = {
        headers: []
    }
    headers.rows().data().toArray().forEach(function (header) {
        profile.headers.push({
            key: unescapeHtml(header[0]),
            value: unescapeHtml(header[1]),
        })
    })
    profile.name = document.getElementById("name").value
    profile.interface_type = document.getElementById("interface_type").value
    profile.from_address = document.getElementById("from").value
    profile.host = document.getElementById("host").value
    profile.username = document.getElementById("username").value
    profile.password = document.getElementById("password").value
    profile.ignore_cert_errors = document.getElementById("ignore_cert_errors").checked
    var saveContext = latestProfileRequest
    setProfileFormDisabled(true)
    if (profileId != -1) {
        profile.id = profileId
        saveRequest = api.SMTPId.put(profile)
        saveRequest
            .then(function () {
                saveRequest = null
                successFlash("Profile edited successfully!")
                load()
                if (saveContext == latestProfileRequest) {
                    dismiss()
                } else if (activeProfileId == profile.id && refreshProfileModal) {
                    refreshProfileModal()
                }
            }, function (error) {
                saveRequest = null
                if (saveContext == latestProfileRequest) {
                    setProfileFormDisabled(false)
                    modalError(requestErrorMessage(error))
                }
            })
    } else {
        // Submit the profile
        saveRequest = api.SMTP.post(profile)
        saveRequest
            .then(function () {
                saveRequest = null
                successFlash("Profile added successfully!")
                load()
                if (saveContext == latestProfileRequest) {
                    dismiss()
                }
            }, function (error) {
                saveRequest = null
                if (saveContext == latestProfileRequest) {
                    setProfileFormDisabled(false)
                    modalError(requestErrorMessage(error))
                }
            })
    }
}

function dismiss() {
    latestProfileRequest++
    activeProfileId = null
    refreshProfileModal = null
    clearModalFlashes()
    document.getElementById("name").value = ""
    document.getElementById("interface_type").value = "SMTP"
    document.getElementById("from").value = ""
    document.getElementById("host").value = ""
    document.getElementById("username").value = ""
    document.getElementById("password").value = ""
    document.getElementById("ignore_cert_errors").checked = true
    if (headers) {
        headers.clear().draw()
    }
    bsModalHide("#modal")
}

var dismissSendTestEmailModal = function () {
    document.querySelectorAll('[id="sendTestEmailModal.flashes"]').forEach(function (container) {
        container.replaceChildren()
    })
    document.getElementById("sendTestModalSubmit").innerHTML = "<i class='fa fa-envelope'></i> Send"
}


var deleteProfile = function (idx) {
    var profileId = profiles[idx].id
    var profileName = profiles[idx].name
    var deleteRequest = null
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the sending profile. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete " + escapeHtml(profileName),
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            if (deleteRequest) {
                return deleteRequest
            }
            deleteRequest = api.SMTPId.delete(profileId)
                .then(undefined, function (error) {
                    deleteRequest = null
                    Swal.showValidationMessage(requestErrorMessage(error))
                })
            return deleteRequest
        }
    }).then(function (result) {
        if (result.value){
            Swal.fire(
                'Sending Profile Deleted!',
                'This sending profile has been deleted!',
                'success'
            );
        }
        // Every button whose label contains "OK", which is what the previous
        // selector matched.
        document.querySelectorAll("button").forEach(function (button) {
            if (button.textContent.includes("OK")) {
                button.addEventListener('click', function () {
                    location.reload()
                })
            }
        })
    })
}

function edit(idx) {
    var profileRequest = ++latestProfileRequest
    headers = new DataTable("#headersTable", {
        destroy: true, // Replace any previously instantiated table
        columnDefs: [{
            orderable: false,
            targets: "no-sort"
        }]
    })

    var profile = {}
    if (idx != -1) {
        profile = profiles[idx]
        var profileId = profile.id
        activeProfileId = profileId
        bindModalSubmit(function () {
            save(profileId)
        })
        document.getElementById("profileModalLabel").textContent = "Edit Sending Profile"
        refreshProfileModal = function () {
            var currentProfile = profiles.find(function (candidate) {
                return candidate.id == profileId
            })
            if (!currentProfile) {
                return
            }
            headers.clear().draw()
            applyProfileFields(currentProfile, currentProfile.name)
            var currentHeaders = currentProfile.headers || []
            currentHeaders.forEach(function (record) {
                addCustomHeader(record.key, record.value)
            })
        }
        refreshProfileModal()
    } else {
        activeProfileId = null
        refreshProfileModal = null
        bindModalSubmit(function () {
            save(-1)
        })
        document.getElementById("profileModalLabel").textContent = "New Sending Profile"
    }
    enableProfileFormAfterPendingSave(profileRequest)
}

function copy(idx) {
    var profileRequest = ++latestProfileRequest
    activeProfileId = null
    refreshProfileModal = null
    bindModalSubmit(function () {
        save(-1)
    })
    var profile = {}
    profile = profiles[idx]
    applyProfileFields(profile, "Copy of " + profile.name)
    enableProfileFormAfterPendingSave(profileRequest)
}

// applyProfileFields writes a stored profile into the modal. The name is
// passed separately because copying prefixes it.
function applyProfileFields(profile, name) {
    document.getElementById("name").value = name
    document.getElementById("interface_type").value = profile.interface_type
    document.getElementById("from").value = profile.from_address
    document.getElementById("host").value = profile.host
    document.getElementById("username").value = profile.username || ""
    // Stored SMTP passwords are write-only. Editing and copying always start
    // with an empty field; an empty update preserves an existing credential.
    document.getElementById("password").value = ""
    document.getElementById("ignore_cert_errors").checked = profile.ignore_cert_errors
}

function load() {
    var loadRequest = ++latestProfileLoadRequest
    document.getElementById("profileTable").style.display = "none"
    document.getElementById("emptyMessage").style.display = "none"
    document.getElementById("loading").style.display = ""
    api.SMTP.get()
        .then(function (ss) {
            if (loadRequest != latestProfileLoadRequest) {
                return
            }
            profiles = ss
            document.getElementById("loading").style.display = "none"
            if (profiles.length > 0) {
                document.getElementById("profileTable").style.display = ""
                profileTable = new DataTable("#profileTable", {
                    destroy: true,
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }]
                });
                profileTable.clear()
                profileRows = []
                profiles.forEach(function (profile, i) {
                    profileRows.push([
                        escapeHtml(profile.name),
                        profile.interface_type,
                        moment(profile.modified_date).format('MMMM Do YYYY, h:mm:ss a'),
                        "<div class='float-end'><span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Edit Profile' onclick='edit(" + i + ")'>\
                    <i class='fa fa-pencil'></i>\
                    </button></span>\
		    <span data-bs-toggle='modal' data-bs-target='#modal'><button class='btn btn-primary' data-bs-toggle='tooltip' data-bs-placement='left' title='Copy Profile' onclick='copy(" + i + ")'>\
                    <i class='fa fa-copy'></i>\
                    </button></span>\
                    <button class='btn btn-danger' data-bs-toggle='tooltip' data-bs-placement='left' title='Delete Profile' onclick='deleteProfile(" + i + ")'>\
                    <i class='fa fa-trash-o'></i>\
                    </button></div>"
                    ])
                })
                profileTable.rows.add(profileRows).draw()
                bsInitTooltips()
            } else {
                document.getElementById("emptyMessage").style.display = ""
            }
        }, function () {
            if (loadRequest != latestProfileLoadRequest) {
                return
            }
            document.getElementById("loading").style.display = "none"
            errorFlash("Error fetching profiles")
        })
}

function addCustomHeader(header, value) {
    // Create new data row.
    var newRow = [
        escapeHtml(header),
        escapeHtml(value),
        '<span style="cursor:pointer;"><i class="fa fa-trash-o"></i></span>'
    ];

    // Check table to see if header already exists.
    var headersTable = headers;
    var existingRowIndex = headersTable
        .column(0) // Email column has index of 2
        .data()
        .indexOf(escapeHtml(header));

    // Update or add new row as necessary.
    if (existingRowIndex >= 0) {
        headersTable
            .row(existingRowIndex, {
                order: "index"
            })
            .data(newRow);
    } else {
        headersTable.row.add(newRow);
    }
    headersTable.draw();
}

// showTestEmailFlash renders the test-email result. The message is inserted as
// text, matching the page-level flash helpers.
function showTestEmailFlash(variantClass, iconClass, message) {
    var alert = document.createElement("div")
    alert.style.textAlign = "center"
    alert.className = "alert " + variantClass
    var icon = document.createElement("i")
    icon.className = "fa " + iconClass
    alert.append(icon, " " + message)
    document.querySelectorAll('[id="sendTestEmailModal.flashes"]').forEach(function (container) {
        container.replaceChildren(alert.cloneNode(true))
    })
}

// The application scripts are plain classic scripts at the end of <body>, so
// the document is still parsing when they run and DOMContentLoaded has not
// fired yet.
document.addEventListener('DOMContentLoaded', function () {
    // Modal dismiss handlers (native Bootstrap 5 events, no jQuery bridge)
    document.getElementById('modal').addEventListener('hidden.bs.modal', function (event) {
        dismiss()
    });
    document.getElementById('sendTestEmailModal').addEventListener('hidden.bs.modal', function (event) {
        dismissSendTestEmailModal()
    })
    // Code to deal with custom email headers
    document.getElementById("addCustomHeader").addEventListener('click', function (event) {
        // The previous handler returned false, which cancelled the default
        // action as well as further propagation.
        event.preventDefault();
        event.stopPropagation();
        headerKey = document.getElementById("headerKey").value;
        headerValue = document.getElementById("headerValue").value;

        if (headerKey == "" || headerValue == "") {
            return;
        }
        addCustomHeader(headerKey, headerValue);
        // Reset user input.
        document.getElementById("headerKey").value = '';
        document.getElementById("headerValue").value = '';
        document.getElementById("headerKey").focus();
    });
    // Handle Deletion. The row is resolved from the clicked icon with a native
    // DOM lookup, because the table's row selector no longer goes through
    // jQuery.
    document.getElementById("headersTable").addEventListener("click", function (event) {
        var icon = event.target.closest("span > i.fa-trash-o")
        if (!icon || !headers) {
            return
        }
        var row = icon.closest("tr")
        if (!row) {
            return
        }
        headers.row(row).remove().draw()
    });
    load()
})
