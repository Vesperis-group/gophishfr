var profiles = []

// headers holds the DataTables instance backing the modal's custom-header list.
// It is recreated every time the modal opens, which is what the destroy option
// did before.
var headers = null

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
        first_name: $("input[name=to_first_name]").val(),
        last_name: $("input[name=to_last_name]").val(),
        email: $("input[name=to_email]").val(),
        position: $("input[name=to_position]").val(),
        url: '',
        smtp: {
            from_address: $("#from").val(),
            host: $("#host").val(),
            username: $("#username").val(),
            password: $("#password").val(),
            ignore_cert_errors: $("#ignore_cert_errors").prop("checked"),
            headers: headerRecords,
        }
    }
    btnHtml = $("#sendTestModalSubmit").html()
    $("#sendTestModalSubmit").html('<i class="fa fa-spinner fa-spin"></i> Sending')
    // Send the test email
    api.send_test_email(test_email_request)
        .done(function (data) {
            $("#sendTestEmailModal\\.flashes").empty().append("<div style=\"text-align:center\" class=\"alert alert-success\">\
	    <i class=\"fa fa-check-circle\"></i> Email Sent!</div>")
            $("#sendTestModalSubmit").html(btnHtml)
        })
        .fail(function (data) {
            $("#sendTestEmailModal\\.flashes").empty().append("<div style=\"text-align:center\" class=\"alert alert-danger\">\
	    <i class=\"fa fa-exclamation-circle\"></i> " + escapeHtml(data.responseJSON.message) + "</div>")
            $("#sendTestModalSubmit").html(btnHtml)
        })
}

// Save attempts to POST to /smtp/
function save(idx) {
    var profile = {
        headers: []
    }
    headers.rows().data().toArray().forEach(function (header) {
        profile.headers.push({
            key: unescapeHtml(header[0]),
            value: unescapeHtml(header[1]),
        })
    })
    profile.name = $("#name").val()
    profile.interface_type = $("#interface_type").val()
    profile.from_address = $("#from").val()
    profile.host = $("#host").val()
    profile.username = $("#username").val()
    profile.password = $("#password").val()
    profile.ignore_cert_errors = $("#ignore_cert_errors").prop("checked")
    if (idx != -1) {
        profile.id = profiles[idx].id
        api.SMTPId.put(profile)
            .done(function (data) {
                successFlash("Profile edited successfully!")
                load()
                dismiss()
            })
            .fail(function (data) {
                modalError(data.responseJSON.message)
            })
    } else {
        // Submit the profile
        api.SMTP.post(profile)
            .done(function (data) {
                successFlash("Profile added successfully!")
                load()
                dismiss()
            })
            .fail(function (data) {
                modalError(data.responseJSON.message)
            })
    }
}

function dismiss() {
    $("#modal\\.flashes").empty()
    $("#name").val("")
    $("#interface_type").val("SMTP")
    $("#from").val("")
    $("#host").val("")
    $("#username").val("")
    $("#password").val("")
    $("#ignore_cert_errors").prop("checked", true)
    if (headers) {
        headers.clear().draw()
    }
    bsModalHide("#modal")
}

var dismissSendTestEmailModal = function () {
    $("#sendTestEmailModal\\.flashes").empty()
    $("#sendTestModalSubmit").html("<i class='fa fa-envelope'></i> Send")
}


var deleteProfile = function (idx) {
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the sending profile. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete " + escapeHtml(profiles[idx].name),
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        preConfirm: function () {
            return new Promise(function (resolve, reject) {
                api.SMTPId.delete(profiles[idx].id)
                    .done(function (msg) {
                        resolve()
                    })
                    .fail(function (data) {
                        reject(data.responseJSON.message)
                    })
            })
        }
    }).then(function (result) {
        if (result.value){
            Swal.fire(
                'Sending Profile Deleted!',
                'This sending profile has been deleted!',
                'success'
            );
        }
        $('button:contains("OK")').on('click', function () {
            location.reload()
        })
    })
}

function edit(idx) {
    headers = new DataTable("#headersTable", {
        destroy: true, // Replace any previously instantiated table
        columnDefs: [{
            orderable: false,
            targets: "no-sort"
        }]
    })

    $("#modalSubmit").unbind('click').click(function () {
        save(idx)
    })
    var profile = {}
    if (idx != -1) {
        $("#profileModalLabel").text("Edit Sending Profile")
        profile = profiles[idx]
        $("#name").val(profile.name)
        $("#interface_type").val(profile.interface_type)
        $("#from").val(profile.from_address)
        $("#host").val(profile.host)
        $("#username").val(profile.username)
        $("#password").val(profile.password)
        $("#ignore_cert_errors").prop("checked", profile.ignore_cert_errors)
        $.each(profile.headers, function (i, record) {
            addCustomHeader(record.key, record.value)
        });
    } else {
        $("#profileModalLabel").text("New Sending Profile")
    }
}

function copy(idx) {
    $("#modalSubmit").unbind('click').click(function () {
        save(-1)
    })
    var profile = {}
    profile = profiles[idx]
    $("#name").val("Copy of " + profile.name)
    $("#interface_type").val(profile.interface_type)
    $("#from").val(profile.from_address)
    $("#host").val(profile.host)
    $("#username").val(profile.username)
    $("#password").val(profile.password)
    $("#ignore_cert_errors").prop("checked", profile.ignore_cert_errors)
}

function load() {
    $("#profileTable").hide()
    $("#emptyMessage").hide()
    $("#loading").show()
    api.SMTP.get()
        .done(function (ss) {
            profiles = ss
            $("#loading").hide()
            if (profiles.length > 0) {
                $("#profileTable").show()
                profileTable = new DataTable("#profileTable", {
                    destroy: true,
                    columnDefs: [{
                        orderable: false,
                        targets: "no-sort"
                    }]
                });
                profileTable.clear()
                profileRows = []
                $.each(profiles, function (i, profile) {
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
                $("#emptyMessage").show()
            }
        })
        .fail(function () {
            $("#loading").hide()
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

$(document).ready(function () {
    // Modal dismiss handlers (native Bootstrap 5 events, no jQuery bridge)
    document.getElementById('modal').addEventListener('hidden.bs.modal', function (event) {
        dismiss()
    });
    document.getElementById('sendTestEmailModal').addEventListener('hidden.bs.modal', function (event) {
        dismissSendTestEmailModal()
    })
    // Code to deal with custom email headers
    $("#addCustomHeader").on('click', function () {
        headerKey = $("#headerKey").val();
        headerValue = $("#headerValue").val();

        if (headerKey == "" || headerValue == "") {
            return false;
        }
        addCustomHeader(headerKey, headerValue);
        // Reset user input.
        $("#headerKey").val('');
        $("#headerValue").val('');
        $("#headerKey").focus();
        return false;
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
